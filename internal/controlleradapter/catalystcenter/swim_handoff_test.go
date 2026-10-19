// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/controllerhandoff"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	coordination "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func handoffFixture(t *testing.T) (*handoffBridge, swimIntent, *ops.IOSXESoftwareUpgrade, *cvk.CiscoDevice, *coordination.Lease) {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = ops.AddToScheme(scheme)
	_ = cvk.AddToScheme(scheme)
	_ = coordination.AddToScheme(scheme)
	source := ops.CatalystCenterImageSource{ControllerName: "catc", ControllerUID: "controller-uid", DeviceID: "device-id", ImageID: "image-id", ImageVersion: "17.18.04.0.759"}
	h := &ops.CatalystCenterSWIMHandoff{ObjectMeta: metav1.ObjectMeta{Name: controllerhandoff.Name("upgrade-uid"), Namespace: "lab", UID: "handoff-uid", Finalizers: []string{controllerhandoff.Finalizer}}, Spec: ops.CatalystCenterSWIMHandoffSpec{UpgradeName: "upgrade", UpgradeUID: "upgrade-uid", DeviceName: "switch", DeviceUID: "device-uid", Serial: "SERIAL", ControllerGeneration: 1, ControllerUsername: "system:serviceaccount:lab:catc-controller-worker", DeviceWorkerUsername: "system:serviceaccount:lab:xe-worker", DeviceWorkerPodUID: "pod-uid", Source: source, TargetVersion: "17.18.04"}}
	up := &ops.IOSXESoftwareUpgrade{ObjectMeta: metav1.ObjectMeta{Name: "upgrade", Namespace: "lab", UID: "upgrade-uid", Annotations: map[string]string{managedprotocol.AnnotationManaged: "true", managedprotocol.AnnotationWorkerUsername: h.Spec.DeviceWorkerUsername, managedprotocol.AnnotationNetworkWorkerPodUID: "pod-uid"}}}
	up.Spec.DeviceRef.Name = "switch"
	up.Spec.ImageSource.CatalystCenter = &source
	up.Spec.TargetVersion = "17.18.04"
	rev := int64(1)
	up.Status.ExecutionModel = ops.UpgradeExecutionModelCatalystCenterV1
	up.Status.ManagerAdmission = &ops.UpgradeManagerAdmissionStatus{ProtocolVersion: ops.ManagedUpgradeProtocolControllerSWIMV1, State: ops.UpgradeManagerAdmissionGranted, PolicyEpoch: 1, ControlRevision: &rev, DeviceUID: "device-uid", NodeUID: "node-uid", PhysicalIdentity: "serial", LeafUID: "upgrade-uid", ReservationID: "reservation"}
	up.Status.ManagerControl = &ops.UpgradeManagerControlStatus{Revision: 1}
	up.Status.ControllerHandoff = &ops.UpgradeControllerHandoffStatus{Name: h.Name, UID: string(h.UID), Stage: "Readiness", Token: "token", WorkerPodUID: "pod-uid", PolicyEpoch: 1, ControlRevision: 1, ExpiresAt: metav1.NewTime(time.Now().Add(time.Minute))}
	d := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "switch", Namespace: "lab", UID: "device-uid"}}
	d.Spec.PhysicalIdentity = "SERIAL"
	d.Spec.Driver = cvk.DeviceDriverXE
	d.Spec.Address = "198.51.100.101"
	leaseName := engine.LeaseName(devicecoordination.DeviceKey("lab", "switch"), devicecoordination.MutationLeaseFamily)
	holder := "software-upgrade/upgrade-uid"
	duration := int32(600)
	lease := &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: leaseName, Namespace: "lab", UID: "lease-uid"}, Spec: coordination.LeaseSpec{HolderIdentity: &holder, RenewTime: &metav1.MicroTime{Time: time.Now()}, LeaseDurationSeconds: &duration}}
	ack := metav1.Now()
	d.Status.MaintenanceSession = &cvk.DeviceMaintenanceSessionStatus{Phase: cvk.DeviceMaintenanceSessionActive, DeviceUID: string(d.UID), NodeUID: "node-uid", AcknowledgedAt: &ack, Operation: cvk.DeviceMaintenanceObjectReference{Namespace: "lab", Name: up.Name, UID: string(up.UID)}, Lease: cvk.DeviceMaintenanceLeaseReference{DeviceMaintenanceObjectReference: cvk.DeviceMaintenanceObjectReference{Namespace: "lab", Name: leaseName, UID: string(lease.UID)}, Holder: holder}}
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "catc", Namespace: "lab", UID: "controller-uid", Generation: 1}}
	nc.Spec.Type = TypeName
	// Use the real worker status type without coupling fixture construction to it.
	raw := []byte(`{"worker":{"name":"catc-controller-worker"}}`)
	if err := json.Unmarshal(raw, &nc.Status); err != nil {
		t.Fatal(err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(h, up, d, nc).WithObjects(h, up, d, lease, nc).Build()
	a := &adapter{key: types.NamespacedName{Namespace: "lab", Name: "catc"}, uid: nc.UID, generation: 1, statusReader: cl, statusWriter: cl}
	return &handoffBridge{a: a, key: kclient.ObjectKeyFromObject(h), uid: string(h.UID)}, handoffIntent(h), up, d, lease
}
func attachHandoffAPI(t *testing.T, b *handoffBridge) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case authPath:
			_, _ = w.Write([]byte(`{"Token":"test"}`))
		case taskPath + "readiness-cancel":
			_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "readiness-cancel", "startTime": time.Now().Add(-time.Minute).UnixMilli(), "endTime": time.Now().Add(-30 * time.Second).UnixMilli(), "isError": false}})
		case devicesPath:
			_, _ = w.Write([]byte(`{"response":[{"id":"device-id","serialNumber":"SERIAL","managementIpAddress":"198.51.100.101","reachabilityStatus":"Reachable"}]}`))
		case imagesPath:
			_, _ = w.Write([]byte(`{"response":[{"imageUuid":"image-id","version":"17.18.04.0.759","applicableDevicesForImage":[{"mdfId":"product"}]}]}`))
		case networkDeviceImagesPath + "device-id":
			_, _ = w.Write([]byte(`{"response":{"id":"device-id","managementAddress":"198.51.100.101","networkDevice":{"id":"product"},"goldenImages":[{"id":"image-id","version":"17.18.04.0.759","imageType":"SYSTEM"}]}}`))
		default:
			t.Errorf("unexpected API submission: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	for _, name := range []string{"username", "password"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	b.a.client = newClient(clientConfig{Endpoint: srv.URL, CredentialPath: dir, InsecureSkipVerify: true, RequestTimeout: time.Second})
}
func TestHandoffLiveGrantAndMutationClaim(t *testing.T) {
	b, i, up, _, _ := handoffFixture(t)
	attachHandoffAPI(t, b)
	ctx := context.Background()
	if token, err := b.Claim(ctx, i, swimReadinessClaimed); err != nil || token != "token" {
		t.Fatalf("readiness claim: %q %v", token, err)
	}
	if err := b.Check(ctx, i, swimDistributionClaimed, "token"); err == nil {
		t.Fatal("readiness grant authorized distribution")
	}
	if err := b.a.statusReader.Get(ctx, kclient.ObjectKeyFromObject(up), up); err != nil {
		t.Fatal(err)
	}
	up.Status.ControllerHandoff.Stage = "Staging"
	if err := b.a.statusWriter.Status().Update(ctx, up); err != nil {
		t.Fatal(err)
	}
	if err := b.Check(ctx, i, swimDistributionClaimed, "token"); err == nil {
		t.Fatal("missing canonical mutation claim accepted")
	}
	up.Status.ManagedMutationClaims = []ops.UpgradeManagedMutationClaimStatus{{Stage: ops.UpgradeManagedMutationStaging, PolicyEpoch: 1, ControlRevision: 1, ReservationID: "reservation"}}
	if err := b.a.statusWriter.Status().Update(ctx, up); err != nil {
		t.Fatal(err)
	}
	if err := b.Check(ctx, i, swimDistributionClaimed, "token"); err != nil {
		t.Fatal(err)
	}
	up.Status.ManagerControl.Cancel = true
	up.Status.ManagerControl.Revision++
	if err := b.a.statusWriter.Status().Update(ctx, up); err != nil {
		t.Fatal(err)
	}
	if err := b.Check(ctx, i, swimDistributionClaimed, "token"); err == nil {
		t.Fatal("cancelled dispatch accepted")
	}
	if err := b.Hold(ctx, i); err != nil {
		t.Fatalf("cancel abandoned existing fence: %v", err)
	}
}
func TestHandoffRejectsStaleAuthority(t *testing.T) {
	for _, kind := range []string{"expired", "epoch", "pod", "parentUID", "controllerUID", "leaseHolder", "leaseUID", "session", "protocol"} {
		t.Run(kind, func(t *testing.T) {
			b, i, up, d, lease := handoffFixture(t)
			ctx := context.Background()
			switch kind {
			case "expired":
				up.Status.ControllerHandoff.ExpiresAt = metav1.NewTime(time.Now().Add(-time.Second))
			case "epoch":
				up.Status.ControllerHandoff.PolicyEpoch++
			case "pod":
				up.Status.ControllerHandoff.WorkerPodUID = "other"
			case "parentUID":
				i.OperationUID = "other"
			case "controllerUID":
				i.Execution.ControllerUID = "other"
			case "leaseHolder":
				v := "other"
				lease.Spec.HolderIdentity = &v
			case "leaseUID":
				d.Status.MaintenanceSession.Lease.UID = "other"
			case "session":
				d.Status.MaintenanceSession.Operation.UID = "other"
			case "protocol":
				up.Status.ManagerAdmission.ProtocolVersion = ops.ManagedUpgradeProtocolRolloutV1
			}
			if err := b.a.statusWriter.Status().Update(ctx, up); err != nil {
				t.Fatal(err)
			}
			if err := b.a.statusWriter.Status().Update(ctx, d); err != nil {
				t.Fatal(err)
			}
			if err := b.a.statusWriter.Update(ctx, lease); err != nil {
				t.Fatal(err)
			}
			if err := b.Check(ctx, i, swimReadinessClaimed, "token"); err == nil {
				t.Fatal("stale authority accepted")
			}
		})
	}
}

func TestHandoffPauseResumeRequiresFreshGrant(t *testing.T) {
	b, i, up, _, _ := handoffFixture(t)
	attachHandoffAPI(t, b)
	ctx := context.Background()
	up.Status.ManagerControl.Revision = 2
	up.Status.ManagerControl.Pause = true
	if err := b.a.statusWriter.Status().Update(ctx, up); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Claim(ctx, i, swimReadinessClaimed); err == nil {
		t.Fatal("pause permitted a new submission")
	}
	if err := b.Hold(ctx, i); err != nil {
		t.Fatalf("pause lost the existing mutation fence: %v", err)
	}
	up.Status.ManagerControl.Revision = 3
	up.Status.ManagerControl.Pause = false
	if err := b.a.statusWriter.Status().Update(ctx, up); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Claim(ctx, i, swimReadinessClaimed); err == nil {
		t.Fatal("resume reused a grant from before the pause")
	}
	revision := int64(3)
	up.Status.ManagerAdmission.ControlRevision = &revision
	up.Status.ControllerHandoff.ControlRevision = revision
	up.Status.ControllerHandoff.Token = "resumed-token"
	if err := b.a.statusWriter.Status().Update(ctx, up); err != nil {
		t.Fatal(err)
	}
	if err := b.Check(ctx, i, swimReadinessClaimed, "token"); err == nil {
		t.Fatal("old token survived fresh authorization")
	}
	if token, err := b.Claim(ctx, i, swimReadinessClaimed); err != nil || token != "resumed-token" {
		t.Fatalf("fresh resume grant rejected: %q %v", token, err)
	}
}
func TestHandoffJournalCASAndRestart(t *testing.T) {
	b, i, _, _, _ := handoffFixture(t)
	ctx := context.Background()
	if err := b.Create(ctx, swimRecord{Intent: i, Phase: swimPending}); err != nil {
		t.Fatal(err)
	}
	before, err := b.Load(ctx, i.OperationUID)
	if err != nil {
		t.Fatal(err)
	}
	after := before
	after.Phase = swimDistributionClaimed
	after.DistributionClaim = "token"
	after.DistributionNotBefore = time.Now()
	if err := b.Replace(ctx, before, after); err != nil {
		t.Fatal(err)
	}
	if err := b.Replace(ctx, before, after); err == nil {
		t.Fatal("stale writer replaced durable journal")
	}
	// Reconstruct the executor from Kubernetes state. No API client is usable:
	// a durable submission marker must never be replayed after a restart.
	e, err := newSWIMExecutor(i.Execution, b, b, b, &client{})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Step(ctx, i); err != nil {
		t.Fatal(err)
	}
	got, err := b.Load(ctx, i.OperationUID)
	if err != nil || got.Phase != swimOutcomeUnknown {
		t.Fatalf("restart lost uncertainty: %+v %v", got, err)
	}
	if _, err = b.Verify(ctx, i); err == nil {
		t.Fatal("controller success replaced native verification")
	}
}

func TestHandoffCompleteControllerFlowRequiresNativeVerification(t *testing.T) {
	b, i, up, _, lease := handoffFixture(t)
	attachHandoffAPI(t, b)
	ctx := context.Background()
	// The API simulator reads the durable journal each tick, not a second
	// execution state machine. Store, authority and verifier are production code.
	mirror := &memorySWIMStore{}
	api := &testModernSWIMAPI{store: mirror}
	executor, err := newSWIMExecutor(i.Execution, b, b, b, api)
	if err != nil {
		t.Fatal(err)
	}
	verified := false
	for n := 0; n < 15; n++ {
		record, loadErr := b.Load(ctx, i.OperationUID)
		if loadErr == nil {
			mirror.record = record
			if err := b.a.statusReader.Get(ctx, kclient.ObjectKeyFromObject(up), up); err != nil {
				t.Fatal(err)
			}
			stage := "Readiness"
			if record.Phase == swimReadyToDistribute {
				stage = "Staging"
				up.Status.StagingRequested = true
			}
			if record.Phase == swimReadyToActivate {
				stage = "PrimaryActivation"
				up.Status.PrimarySupervisorActivationRequested = true
			}
			up.Status.ControllerHandoff.Stage = stage
			if stage != "Readiness" {
				up.Status.ManagedMutationClaims = append(up.Status.ManagedMutationClaims, ops.UpgradeManagedMutationClaimStatus{Stage: ops.UpgradeManagedMutationStage(stage), PolicyEpoch: 1, ControlRevision: 1, ReservationID: "reservation", ClaimedAt: metav1.Now()})
			}
			if record.Phase == swimVerifying {
				if err := executor.Step(ctx, i); err == nil {
					t.Fatal("task success was accepted without native proof")
				}
				// metav1.Time serializes whole seconds. Keep native evidence strictly
				// newer than the persisted activation marker, as real device boot does.
				if delay := time.Until(record.ActivationNotBefore.Add(time.Second)); delay > 0 {
					time.Sleep(delay)
				}
				up.Status.ControllerHandoff.VerifiedVersion = "17.18.04.0.759.1776157760"
				now := metav1.Now()
				up.Status.ControllerHandoff.VerifiedAt = &now
				verified = true
			}
			if err := b.a.statusWriter.Status().Update(ctx, up); err != nil {
				t.Fatal(err)
			}
			if record.Phase == swimSucceeded {
				break
			}
		}
		if err := executor.Step(ctx, i); err != nil {
			t.Fatalf("tick %d phase %s: %v", n, record.Phase, err)
		}
	}
	record, err := b.Load(ctx, i.OperationUID)
	if err != nil || record.Phase != swimSucceeded || !verified || api.readiness != 2 || api.distributions != 1 || api.activations != 1 {
		t.Fatalf("incomplete handoff: %+v readiness=%d distribution=%d activation=%d err=%v", record, api.readiness, api.distributions, api.activations, err)
	}
	if len(record.ReadinessHistory) != 1 || record.ReadinessHistory[0].Task != "readiness-1" || record.ReadinessHistory[0].Stage != string(swimReadyToDistribute) {
		t.Fatal("durable journal lost the distribution readiness receipt")
	}
	if err := b.a.statusReader.Get(ctx, kclient.ObjectKeyFromObject(lease), lease); err != nil {
		t.Fatal(err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != "software-upgrade/upgrade-uid" {
		t.Fatal("controller released a device-owned lease")
	}
}

func TestHandoffRejectsDifferentCanonicalPhysicalIdentity(t *testing.T) {
	b, i, up, _, _ := handoffFixture(t)
	if _, err := b.Claim(context.Background(), i, swimReadinessClaimed); err != nil {
		t.Fatalf("uppercase declaration must match canonical manager identity: %v", err)
	}
	up.Status.ManagerAdmission.PhysicalIdentity = "other-serial"
	if err := b.a.statusWriter.Status().Update(context.Background(), up); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Claim(context.Background(), i, swimReadinessClaimed); err == nil {
		t.Fatal("different physical identity accepted")
	}
}

func TestHandoffCancelledPreparationCannotDispatch(t *testing.T) {
	b, _, up, _, _ := handoffFixture(t)
	attachHandoffAPI(t, b)
	ctx := context.Background()
	h, err := b.object(ctx)
	if err != nil {
		t.Fatal(err)
	}
	up.Spec.ImageSource.CatalystCenter.Preparation = &ops.SWIMPreparationPolicyRef{Name: "policy", UID: "policy-uid", SHA256: "digest"}
	h.Spec.Source = *up.Spec.ImageSource.CatalystCenter
	if err = b.a.statusWriter.Update(ctx, up); err != nil {
		t.Fatal(err)
	}
	if err = b.a.statusWriter.Update(ctx, h); err != nil {
		t.Fatal(err)
	}
	up.Status.ExecutionModel = ops.UpgradeExecutionModelCatalystCenterPreparationV1
	up.Status.ManagerControl.Cancel = true
	up.Status.ManagerAdmission.ProtocolVersion = ops.ManagedUpgradeProtocolControllerPreparationV1
	done := metav1.NewTime(time.Now().Add(-2 * time.Minute))
	up.Status.ControllerHandoff.Preparation = []ops.SWIMDevicePreparation{{ID: "prep", Stage: "ReadyToDistribute", Phase: "Complete", CompletedAt: &done, PolicySHA256: "digest"}}
	if err = b.a.statusWriter.Status().Update(ctx, up); err != nil {
		t.Fatal(err)
	}
	h.Status = ops.CatalystCenterSWIMHandoffStatus{Phase: "CheckingReadiness", ReadinessFor: "ReadyToDistribute", ReadinessTask: "readiness-cancel", ReadinessNotBefore: &done, InventorySyncs: []ops.SWIMInventorySync{{Stage: "ReadyToDistribute", PreparationID: "prep", Task: "sync", CompletedAt: &done}}}
	if err = b.a.statusWriter.Status().Update(ctx, h); err != nil {
		t.Fatal(err)
	}
	reconciler := &handoffReconciler{a: b.a}
	for n := 0; n < 2; n++ {
		if _, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: b.key}); err != nil {
			t.Fatal(err)
		}
	}
	h, err = b.object(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h.Status.Phase != "PreparationCancelled" || h.Status.DistributionClaim != "" || h.Status.ActivationClaim != "" {
		t.Fatalf("unexpected cancellation: %+v", h.Status)
	}
}
