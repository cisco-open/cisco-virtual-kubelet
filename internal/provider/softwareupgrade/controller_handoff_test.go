// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package softwareupgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
	"reflect"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/controllerhandoff"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/mutationguard"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestControllerHandoffStageClaimsAndReceiptFence(t *testing.T) {
	up := managedTestLeaf("swim")
	up.Spec.ImageSource = ops.UpgradeImageSource{CatalystCenter: &ops.CatalystCenterImageSource{ControllerName: "catc", ControllerUID: "controller-uid", DeviceID: "device-id", ImageID: "image-id", ImageVersion: "17.18.04.0.759"}}
	up.Status.ExecutionModel = ops.UpgradeExecutionModelCatalystCenterV1
	up.Status.ManagerAdmission.ProtocolVersion = ops.ManagedUpgradeProtocolControllerSWIMV1
	up.Status.PreviousVersion = "17.18.03"
	up.Status.Phase = ops.UpgradePhaseResolving
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "catc", Namespace: up.Namespace, UID: "controller-uid", Generation: 1}}
	nc.Spec.Type = controllerhandoff.ControllerType
	if err := json.Unmarshal([]byte(`{"worker":{"name":"catc-controller-worker"}}`), &nc.Status); err != nil {
		t.Fatal(err)
	}
	h := &ops.CatalystCenterSWIMHandoff{ObjectMeta: metav1.ObjectMeta{Name: controllerhandoff.Name(string(up.UID)), Namespace: up.Namespace, UID: "handoff-uid"}, Spec: ops.CatalystCenterSWIMHandoffSpec{UpgradeName: up.Name, UpgradeUID: string(up.UID), DeviceName: managedTestDeviceName, DeviceUID: managedTestDeviceUID, Serial: managedTestPhysicalIdentity, ControllerGeneration: 1, ControllerUsername: "system:serviceaccount:" + up.Namespace + ":catc-controller-worker", DeviceWorkerUsername: up.Annotations[managedprotocol.AnnotationWorkerUsername], DeviceWorkerPodUID: managedTestWorkerPodUID, Source: *up.Spec.ImageSource.CatalystCenter, TargetVersion: up.Spec.TargetVersion}, Status: ops.CatalystCenterSWIMHandoffStatus{Phase: "ReadyToDistribute"}}
	r := newManagedTestReconciler(t, up, nil, nc, h)
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: up.Namespace}
	r.BeforeMutation = func(context.Context, *ops.IOSXESoftwareUpgrade) error { return nil }
	ctx := context.Background()
	// First persist the existing canonical stage claim, then publish a dispatch
	// grant on a separate reconciliation. No gNOI mutation client is configured.
	for n := 0; n < 3; n++ {
		if _, err := r.runControllerHandoff(ctx, up, managedTestTime); err != nil {
			t.Fatal(err)
		}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(up), up); err != nil {
			t.Fatal(err)
		}
	}
	if !up.Status.StagingRequested || up.Status.ControllerHandoff == nil || up.Status.ControllerHandoff.Stage != "Staging" || len(up.Status.ManagedMutationClaims) != 1 {
		t.Fatalf("distribution not bound to parent claim: decision=%+v status=%+v", r.evaluateManagedLeaf(ctx, up), up.Status)
	}
	// Resume changes the dispatch grant, never the original durable claim.
	originalClaim := up.Status.ManagedMutationClaims[0]
	up.Status.ManagerControl.Revision++
	up.Status.ManagerControl.Pause = true
	revision := up.Status.ManagerControl.Revision
	up.Status.ManagerAdmission.ControlRevision = &revision
	if err := r.Client.Status().Update(ctx, up); err != nil {
		t.Fatal(err)
	}
	previousToken := up.Status.ControllerHandoff.Token
	if _, err := r.runControllerHandoff(ctx, up, managedTestTime); err != nil {
		t.Fatal(err)
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(up), up); err != nil {
		t.Fatal(err)
	}
	if up.Status.ControllerHandoff.Token != previousToken {
		t.Fatal("paused parent renewed grant")
	}
	up.Status.ManagerControl.Pause = false
	if err := r.Client.Status().Update(ctx, up); err != nil {
		t.Fatal(err)
	}
	if _, err := r.runControllerHandoff(ctx, up, managedTestTime); err != nil {
		t.Fatal(err)
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(up), up); err != nil {
		t.Fatal(err)
	}
	if up.Status.ControllerHandoff.ControlRevision != revision || up.Status.ControllerHandoff.Token == previousToken || !reflect.DeepEqual(up.Status.ManagedMutationClaims[0], originalClaim) || len(up.Status.ManagedMutationClaims) != 1 {
		t.Fatal("resume failed to renew grant while preserving original claim")
	}
	grant := *up.Status.ControllerHandoff
	h.Status.Phase = "DistributionClaimed"
	if err := r.Client.Update(ctx, h); err != nil {
		t.Fatal(err)
	}
	if _, err := r.runControllerHandoff(ctx, up, managedTestTime); err != nil {
		t.Fatal(err)
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(up), up); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*up.Status.ControllerHandoff, grant) {
		t.Fatal("in-flight dispatch grant was overwritten")
	}
	h.Status.Phase = "ReadyToActivate"
	if err := r.Client.Update(ctx, h); err != nil {
		t.Fatal(err)
	}
	up.Status.ManagerControl.Cancel = true
	if err := r.Client.Status().Update(ctx, up); err != nil {
		t.Fatal(err)
	}
	if _, err := r.runControllerHandoff(ctx, up, managedTestTime); err != nil {
		t.Fatal(err)
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(up), up); err != nil {
		t.Fatal(err)
	}
	if up.Status.PrimarySupervisorActivationRequested {
		t.Fatal("cancelled parent claimed activation")
	}
}

func TestControllerPreparationCancellationVerifiesNativeAbsence(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			up := managedTestLeaf("cancel-preparation")
			up.Status.StagingRequested = true
			up.Status.ManagedMutationClaims = []ops.UpgradeManagedMutationClaimStatus{managedTestMutationClaim(ops.UpgradeManagedMutationStaging)}
			up.Status.Phase = ops.UpgradePhaseStaging
			up.Status.PreviousVersion = "17.15.01a"
			up.Spec.ImageSource = ops.UpgradeImageSource{CatalystCenter: &ops.CatalystCenterImageSource{ControllerName: "catc", ControllerUID: "controller-uid", DeviceID: "device-id", ImageID: "image-id", ImageVersion: "17.18.04.0.759", Preparation: &ops.SWIMPreparationPolicyRef{Name: "policy", UID: "policy-uid", SHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}}}
			up.Status.ExecutionModel = ops.UpgradeExecutionModelCatalystCenterPreparationV1
			up.Status.ManagerAdmission.ProtocolVersion = ops.ManagedUpgradeProtocolControllerPreparationV1
			up.Status.ManagerControl.Cancel = true
			nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "catc", Namespace: up.Namespace, UID: "controller-uid", Generation: 1}}
			nc.Spec.Type = controllerhandoff.ControllerType
			if err := json.Unmarshal([]byte(`{"worker":{"name":"catc-controller-worker"}}`), &nc.Status); err != nil {
				t.Fatal(err)
			}
			now := metav1.NewTime(managedTestTime)
			h := &ops.CatalystCenterSWIMHandoff{ObjectMeta: metav1.ObjectMeta{Name: controllerhandoff.Name(string(up.UID)), Namespace: up.Namespace, UID: "handoff-uid"}, Spec: ops.CatalystCenterSWIMHandoffSpec{UpgradeName: up.Name, UpgradeUID: string(up.UID), DeviceName: managedTestDeviceName, DeviceUID: managedTestDeviceUID, Serial: managedTestPhysicalIdentity, ControllerGeneration: 1, ControllerUsername: "system:serviceaccount:" + up.Namespace + ":catc-controller-worker", DeviceWorkerUsername: up.Annotations[managedprotocol.AnnotationWorkerUsername], DeviceWorkerPodUID: managedTestWorkerPodUID, Source: *up.Spec.ImageSource.CatalystCenter, TargetVersion: up.Spec.TargetVersion}, Status: ops.CatalystCenterSWIMHandoffStatus{Phase: "PreparationCancelled", ReadinessFor: "ReadyToDistribute", ReadinessTask: "readiness", ReadinessNotBefore: &now, InventorySyncs: []ops.SWIMInventorySync{{Stage: "ReadyToDistribute", PreparationID: "prep", Task: "sync", CompletedAt: &now}}}}
			path := "flash:gNOI_iosxe_17.18.02.bin"
			up.Status.ControllerHandoff = &ops.UpgradeControllerHandoffStatus{Name: h.Name, UID: string(h.UID), Preparation: []ops.SWIMDevicePreparation{{ID: "prep", Stage: "ReadyToDistribute", Phase: "Complete", CompletedAt: &now, PolicySHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", Files: []ops.SWIMPreparationFile{{Path: path, ClaimedAt: &now, RemovedAt: &now}}}}}
			r := newManagedTestReconciler(t, up, nil, nc, h)
			r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: up.Namespace}
			r.BeforeMutation = func(context.Context, *ops.IOSXESoftwareUpgrade) error { return nil }
			rig := newRig(t)
			r.GNOI = &staticGNOI{c: rig.client}
			backend := &swimPreparationBackendStub{snapshot: softwarelifecycle.SWIMFlashSnapshot{FreeBytes: 5000, Files: map[string]uint64{}}}
			if present {
				backend.snapshot.Files[path] = 1024
			}
			r.Lifecycle = backend
			if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), up); err != nil {
				t.Fatal(err)
			}
			_, err := r.runControllerHandoff(context.Background(), up, managedTestTime)
			if present {
				if err == nil {
					t.Fatal("present archive settled cleanup")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Client.Get(context.Background(), client.ObjectKeyFromObject(up), up); err != nil {
				t.Fatal(err)
			}
			if up.Status.Phase != ops.UpgradePhaseCancelled {
				t.Fatalf("not cancelled: %+v", up.Status)
			}
			if mutationguard.UpgradeRequiresQuarantineAt(up, managedTestTime) {
				t.Fatal("verified preparation cancellation remains quarantined")
			}
			up.Status.Conditions = nil
			if !mutationguard.UpgradeRequiresQuarantineAt(up, managedTestTime) {
				t.Fatal("unverified cancellation released quarantine")
			}
			if backend.removals != 0 {
				t.Fatal("cancellation replayed cleanup")
			}
		})
	}
}
