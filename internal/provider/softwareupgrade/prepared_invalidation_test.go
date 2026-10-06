// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package softwareupgrade

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
	"github.com/cisco/virtual-kubelet-cisco/internal/workloaddrain"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func invalidationTestLeaf(t *testing.T) *ops.IOSXESoftwareUpgrade {
	t.Helper()
	u := managedCancellationAuditRecord("prepared-recovery", false)
	u.Spec.Strategy = ops.UpgradeStrategyPrepareOnly
	u.Status.Phase = ops.UpgradePhasePrepared
	u.Status.CompletionTime = &metav1.Time{Time: managedTestTime.Add(-time.Minute)}
	u.Status.PrimarySupervisorInstalled = true
	u.Status.PrimarySupervisorInstallRequested = true
	u.Status.ManagerControl.Revision = 8
	u.Status.ManagerAdmission.TopologyLockID = strings.Repeat("1", 32)
	u.Status.WorkerControl.ObservedControlRevision = 8
	u.Status.Conditions = []metav1.Condition{{Type: conditionTypeMutationSettled, Status: metav1.ConditionTrue, Reason: "Prepared", LastTransitionTime: *u.Status.CompletionTime}}
	a := u.Status.ManagerAdmission
	u.Status.ManagedMutationClaims = []ops.UpgradeManagedMutationClaimStatus{{Stage: ops.UpgradeManagedMutationPrimaryInstall, ReservationID: a.ReservationID, PolicyEpoch: a.PolicyEpoch, ClaimedAt: metav1.NewTime(managedTestTime.Add(-time.Hour))}}
	receipt := &ops.UpgradePreparedReceiptStatus{
		ProtocolVersion: "prepare-v1", UpgradeUID: string(u.UID), DeviceUID: a.DeviceUID, NodeUID: a.NodeUID, PhysicalIdentity: a.PhysicalIdentity, DeviceGeneration: a.DeviceGeneration,
		CampaignUID: a.CampaignUID, PlanHash: a.PlanHash, ManagedProtocolVersion: a.ProtocolVersion, PolicyUID: a.PolicyUID, PolicyResourceVersion: a.PolicyResourceVersion, PolicyEpoch: a.PolicyEpoch,
		WorkerRevision: managedTestWorkerRevision, SourceDigest: "sha256:" + strings.Repeat("a", 64), SourceSize: 1024, SourceIdentityHash: "sha256:" + strings.Repeat("b", 64), TrustIdentityHash: "sha256:" + strings.Repeat("c", 64),
		ContentBinding: preparedContentBindingV1, TargetVersion: "17.18.02", ValidatedVersion: "17.18.02", RunningVersion: "17.18.03", PrimarySupervisorInstalled: true,
		InstallStartedAt: metav1.NewTime(managedTestTime.Add(-time.Hour)), PreparedAt: *u.Status.CompletionTime, ManagedMutationClaims: u.Status.ManagedMutationClaims,
	}
	receipt.ReceiptHash, _ = PreparedReceiptHash(*receipt)
	u.Status.PreparedReceipt = receipt
	u.Status.ManagerInvalidation = &ops.UpgradePreparedInvalidationRequest{ReceiptHash: receipt.ReceiptHash, PlanHash: receipt.PlanHash, CampaignUID: receipt.CampaignUID, ControlRevision: 8, RequestedAt: metav1.NewTime(managedTestTime), RequestedBy: "operator", Reason: "abandon preparation"}
	return u
}

func TestPreparedInvalidationRequiresExactSettledAuthorityAndProof(t *testing.T) {
	u := invalidationTestLeaf(t)
	if err := ValidatePreparedInvalidationRequest(u); err != nil {
		t.Fatal(err)
	}
	if PreparedReceiptInvalidated(u) {
		t.Fatal("request alone released receipt")
	}
	u.Status.Phase = ops.UpgradePhasePreparedInvalidated
	u.Status.PreparedInvalidation = &ops.UpgradePreparedInvalidationStatus{RequestHash: PreparedInvalidationRequestHash(u.Status.ManagerInvalidation), NativeEvidenceHash: "sha256:" + strings.Repeat("e", 64), ObservedAt: metav1.NewTime(managedTestTime), WorkerRevision: managedTestWorkerRevision, WorkerPodUID: managedTestWorkerPodUID, RunningVersion: "17.18.03", TargetState: "Installed"}
	if !PreparedReceiptInvalidated(u) {
		t.Fatal("valid proof rejected")
	}
	for name, mutate := range map[string]func(*ops.IOSXESoftwareUpgrade){
		"request absent": func(x *ops.IOSXESoftwareUpgrade) { x.Status.ManagerInvalidation = nil },
		"proof absent":   func(x *ops.IOSXESoftwareUpgrade) { x.Status.PreparedInvalidation = nil },
		"phase only":     func(x *ops.IOSXESoftwareUpgrade) { x.Status.PreparedInvalidation.RequestHash = "" },
		"wrong receipt": func(x *ops.IOSXESoftwareUpgrade) {
			x.Status.ManagerInvalidation.ReceiptHash = "sha256:" + strings.Repeat("0", 64)
		},
		"uncancelled":        func(x *ops.IOSXESoftwareUpgrade) { x.Status.ManagerControl.Cancel = false },
		"unsettled":          func(x *ops.IOSXESoftwareUpgrade) { x.Status.Conditions = nil },
		"stale ack":          func(x *ops.IOSXESoftwareUpgrade) { x.Status.WorkerControl.ObservedControlRevision-- },
		"activation claimed": func(x *ops.IOSXESoftwareUpgrade) { x.Status.PrimarySupervisorActivationRequested = true },
		"running changed":    func(x *ops.IOSXESoftwareUpgrade) { x.Status.PreparedInvalidation.RunningVersion = "17.18.02" },
		"stale native": func(x *ops.IOSXESoftwareUpgrade) {
			x.Status.PreparedInvalidation.ObservedAt = metav1.NewTime(managedTestTime.Add(-time.Second))
		},
		"target uncertain": func(x *ops.IOSXESoftwareUpgrade) { x.Status.PreparedInvalidation.TargetState = "InProgress" },
		"replacement leaf": func(x *ops.IOSXESoftwareUpgrade) { x.UID = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			x := u.DeepCopy()
			mutate(x)
			if PreparedReceiptInvalidated(x) {
				t.Fatal("unsafe release")
			}
		})
	}
}

func withSettledInvalidationDrain(t *testing.T, u *ops.IOSXESoftwareUpgrade) {
	t.Helper()
	u.Status.ManagerControl.Revision = 0
	u.Status.WorkerControl.ObservedControlRevision = 0
	u.Status.ManagerControl.Cancel = false
	u.Status.ManagerInvalidation.ControlRevision = u.Status.ManagerControl.Revision + 1
	revision := u.Status.ManagerControl.Revision
	u.Status.ManagerAdmission.ControlRevision = &revision
	u.Status.ManagerDrain = &ops.UpgradeManagerDrainStatus{
		ProtocolVersion: ops.ManagedDrainProtocolPDBV1, State: ops.UpgradeManagerDrainSettled,
		SessionToken: "6a144e34-cd5d-4479-905d-6023cc60f42a", NodeUID: u.Status.ManagerAdmission.NodeUID,
		ReservationID: u.Status.ManagerAdmission.ReservationID, PolicyEpoch: u.Status.ManagerAdmission.PolicyEpoch,
		ControlRevision: revision, StartedAt: metav1.NewTime(managedTestTime.Add(-2 * time.Hour)),
		DrainDeadline: metav1.NewTime(managedTestTime.Add(-time.Hour)), UpdatedAt: *u.Status.CompletionTime,
		RecoveryDeadline: &metav1.Time{Time: managedTestTime.Add(time.Hour)},
	}
	pod := ops.UpgradeDrainPodStatus{
		Namespace: "apps", Name: "web", UID: "old-pod", Phase: ops.UpgradeDrainPodComplete,
		Controller:                    ops.UpgradeDrainObjectReference{APIVersion: "apps/v1", Kind: "ReplicaSet", Namespace: "apps", Name: "web", UID: "rs", Generation: 1},
		TerminationGracePeriodSeconds: 30,
		DeviceCleanInventoryRevision:  2,
	}
	var err error
	pod.EligibilityHash, err = workloaddrain.EligibilityHash(&pod)
	if err != nil {
		t.Fatal(err)
	}
	u.Status.ManagerDrain.Pods = []ops.UpgradeDrainPodStatus{pod}
}

func TestPreparedInvalidationPreservesSettledDrainControl(t *testing.T) {
	u := invalidationTestLeaf(t)
	withSettledInvalidationDrain(t, u)
	revision := u.Status.ManagerControl.Revision
	before := u.DeepCopy()
	if err := ValidatePreparedInvalidationRequest(u); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, u) {
		t.Fatal("validation rewrote settled drain audit")
	}
	for name, change := range map[string]func(*ops.IOSXESoftwareUpgrade){
		"not settled":       func(x *ops.IOSXESoftwareUpgrade) { x.Status.ManagerDrain.State = ops.UpgradeManagerDrainRecovering },
		"unknown protocol":  func(x *ops.IOSXESoftwareUpgrade) { x.Status.ManagerDrain.ProtocolVersion = "future" },
		"wrong node":        func(x *ops.IOSXESoftwareUpgrade) { x.Status.ManagerDrain.NodeUID = "other" },
		"wrong reservation": func(x *ops.IOSXESoftwareUpgrade) { x.Status.ManagerDrain.ReservationID = "other" },
		"wrong epoch":       func(x *ops.IOSXESoftwareUpgrade) { x.Status.ManagerDrain.PolicyEpoch++ },
		"wrong control":     func(x *ops.IOSXESoftwareUpgrade) { x.Status.ManagerDrain.ControlRevision++ },
		"stale request":     func(x *ops.IOSXESoftwareUpgrade) { x.Status.ManagerInvalidation.ControlRevision = revision },
		"incomplete pod": func(x *ops.IOSXESoftwareUpgrade) {
			x.Status.ManagerDrain.Pods = []ops.UpgradeDrainPodStatus{{UID: "pending", Phase: ops.UpgradeDrainPodEvictionRequested}}
		},
		"paused":              func(x *ops.IOSXESoftwareUpgrade) { x.Status.ManagerControl.Pause = true },
		"missing ack":         func(x *ops.IOSXESoftwareUpgrade) { x.Status.WorkerControl = nil },
		"changed eligibility": func(x *ops.IOSXESoftwareUpgrade) { x.Status.ManagerDrain.Pods[0].UID = "replacement" },
	} {
		t.Run(name, func(t *testing.T) {
			x := u.DeepCopy()
			change(x)
			if ValidatePreparedInvalidationRequest(x) == nil {
				t.Fatal("unsafe settled-drain retirement accepted")
			}
		})
	}
}

type fakeRetirementObserver struct {
	fakeLifecycle
	err     error
	before  func(context.Context)
	request softwarelifecycle.PreparationRetirementRequest
}

func (f *fakeRetirementObserver) ObservePreparationRetirement(ctx context.Context, request softwarelifecycle.PreparationRetirementRequest) (softwarelifecycle.PreparationRetirementObservation, error) {
	f.request = request
	if f.before != nil {
		f.before(ctx)
	}
	return softwarelifecycle.PreparationRetirementObservation{TargetState: softwarelifecycle.InventoryStateInstalled, EvidenceHash: "sha256:" + strings.Repeat("d", 64)}, f.err
}

func TestPreparedInvalidationRetainsOwnershipAcrossLeaseAndCASRaces(t *testing.T) {
	for _, name := range []string{"foreign lease", "expired observation", "changed request"} {
		t.Run(name, func(t *testing.T) {
			u := invalidationTestLeaf(t)
			r := newManagedTestReconciler(t, u, nil)
			if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(u), u); err != nil {
				t.Fatal(err)
			}
			r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: u.Namespace, TTL: time.Minute}
			rig := newRig(t)
			rig.os.verifyVersion = "17.18.03"
			r.GNOI = &staticGNOI{c: rig.client}
			observer := &fakeRetirementObserver{}
			r.Lifecycle = observer
			switch name {
			case "foreign lease":
				if _, err := r.MutationLeaser.Acquire(context.Background(), r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily, "another-operation"); err != nil {
					t.Fatal(err)
				}
			case "expired observation":
				r.MutationLeaser.TTL = time.Second
				observer.before = func(ctx context.Context) { <-ctx.Done() }
			case "changed request":
				observer.before = func(context.Context) {
					current := u.DeepCopy()
					if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(u), current); err != nil {
						t.Fatal(err)
					}
					current.Status.ManagerControl.Revision++
					if err := r.Client.Status().Update(context.Background(), current); err != nil {
						t.Fatal(err)
					}
				}
			}
			_, err := r.reconcilePreparedInvalidation(context.Background(), u)
			if name == "expired observation" && err == nil {
				t.Fatal("expired observation did not fail")
			}
			var got ops.IOSXESoftwareUpgrade
			if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(u), &got); err != nil {
				t.Fatal(err)
			}
			if got.Status.PreparedInvalidation != nil || PreparedReceiptInvalidated(&got) {
				t.Fatal("raced observation released ownership")
			}
			if rig.os.installCalls != 0 || rig.os.activateCalls != 0 {
				t.Fatal("recovery mutated device")
			}
			if name == "foreign lease" {
				result, err := r.MutationLeaser.Acquire(context.Background(), r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily, "probe")
				if err != nil || result.Owned || result.Holder != "another-operation" {
					t.Fatalf("foreign lease was altered: %+v %v", result, err)
				}
			}
		})
	}
}

func TestPreparedInvalidationWorkerObservesWithoutMutation(t *testing.T) {
	for _, drained := range []bool{false, true} {
		for _, blocked := range []bool{false, true} {
			t.Run(fmt.Sprintf("drained=%t/blocked=%t", drained, blocked), func(t *testing.T) {
				u := invalidationTestLeaf(t)
				if drained {
					withSettledInvalidationDrain(t, u)
				}
				r := newManagedTestReconciler(t, u, nil)
				if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(u), u); err != nil {
					t.Fatal(err)
				}
				r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: u.Namespace, TTL: time.Minute}
				rig := newRig(t)
				rig.os.verifyVersion = "17.18.03"
				r.GNOI = &staticGNOI{c: rig.client}
				observer := &fakeRetirementObserver{}
				if blocked {
					observer.err = fmt.Errorf("installer still active")
				}
				r.Lifecycle = observer
				_, err := r.reconcilePreparedInvalidation(context.Background(), u)
				if (err != nil) != blocked {
					t.Fatalf("error=%v", err)
				}
				var got ops.IOSXESoftwareUpgrade
				if err := r.Client.Get(context.Background(), client.ObjectKeyFromObject(u), &got); err != nil {
					t.Fatal(err)
				}
				if PreparedReceiptInvalidated(&got) == blocked {
					t.Fatalf("unexpected release: %+v", got.Status.PreparedInvalidation)
				}
				if !reflect.DeepEqual(got.Status.PreparedReceipt, u.Status.PreparedReceipt) {
					t.Fatal("receipt changed")
				}
				if !reflect.DeepEqual(got.Status.ManagerDrain, u.Status.ManagerDrain) ||
					!reflect.DeepEqual(got.Status.ManagerControl, u.Status.ManagerControl) ||
					!reflect.DeepEqual(got.Status.ManagedMutationClaims, u.Status.ManagedMutationClaims) {
					t.Fatal("observation changed immutable drain or mutation audit")
				}
				receipt := u.Status.PreparedReceipt
				if observer.request.TargetVersion != receipt.ValidatedVersion || observer.request.RunningVersion != receipt.RunningVersion ||
					observer.request.SourceSize != receipt.SourceSize || !observer.request.InstallStartedAt.Equal(receipt.InstallStartedAt.Time) ||
					!observer.request.PreparedAt.Equal(receipt.PreparedAt.Time) {
					t.Fatal("native observer did not receive the immutable receipt evidence")
				}
				if rig.os.installCalls != 0 || rig.os.activateCalls != 0 {
					t.Fatal("recovery mutated device")
				}
			})
		}
	}
}
