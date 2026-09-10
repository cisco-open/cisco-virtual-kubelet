// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package softwareupgrade

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	coordv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
)

func attachMutationLeaser(r *Reconciler, up *opsv1alpha1.IOSXESoftwareUpgrade) {
	r.DeviceNamespace = up.Namespace
	r.MutationLeaser = &engine.FamilyLeaser{Client: r.Client, Namespace: up.Namespace, TTL: 26 * time.Hour}
}

func assertMutationLease(t *testing.T, r *Reconciler, want bool) {
	t.Helper()
	var lease coordv1.Lease
	err := r.Client.Get(context.Background(), client.ObjectKey{
		Namespace: r.MutationLeaser.Namespace,
		Name:      engine.LeaseName(r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily),
	}, &lease)
	if want && err != nil || !want && !apierrors.IsNotFound(err) {
		t.Fatalf("mutation Lease present=%t: %v", want, err)
	}
}

func TestPermanentObservationFailuresRetainActivationFence(t *testing.T) {
	for _, phase := range []opsv1alpha1.UpgradePhase{
		opsv1alpha1.UpgradePhaseAwaitingReachability,
		opsv1alpha1.UpgradePhaseVerifying,
		opsv1alpha1.UpgradePhaseActivating, // accepted NoReboot
	} {
		for _, code := range []codes.Code{codes.Unauthenticated, codes.PermissionDenied, codes.Unimplemented, codes.FailedPrecondition, codes.NotFound, codes.InvalidArgument} {
			t.Run(string(phase)+"/"+code.String(), func(t *testing.T) {
				rig := newRig(t)
				rig.os.verifyErr = status.Error(code, "OS.Verify unavailable after activation")
				up := newUpgrade("observation-failure", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
					up.Finalizers = []string{Finalizer}
					up.Status.Phase = phase
					up.Status.PrimarySupervisorActivationRequested = true
					up.Status.ActivationStartTime = &metav1.Time{Time: time.Unix(1_699_999_995, 0)}
					if phase == opsv1alpha1.UpgradePhaseActivating {
						up.Spec.Strategy = opsv1alpha1.UpgradeStrategyNoReboot
						up.Status.NoRebootActivationAccepted = true
					}
				})
				r := newReconciler(t, rig, up)
				attachMutationLeaser(r, up)
				got := runReconcile(t, r, up, 1)
				if got.Status.Phase != opsv1alpha1.UpgradePhaseFailed || rig.os.activateCalls != 0 {
					t.Fatalf("phase=%s activate calls=%d", got.Status.Phase, rig.os.activateCalls)
				}
				assertMutationLease(t, r, true)
			})
		}
	}
}

func TestActivationClaimReplacesPriorCompletionEvidence(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "indeterminate", true: "definitively rejected"}[rejected], func(t *testing.T) {
			rig := newRig(t)
			rig.os.activateErr = status.Error(codes.Unavailable, "response lost")
			if rejected {
				rig.os.activateErr = status.Error(codes.PermissionDenied, "activation rejected")
			}
			up := newUpgrade("activation-evidence", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
				up.Finalizers = []string{Finalizer}
				up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
				up.Status.ValidatedVersion = up.Spec.TargetVersion
				up.Status.Conditions = []metav1.Condition{{Type: conditionTypeMutationSettled, Status: metav1.ConditionTrue, Reason: "InstallCompleted"}}
			})
			r := newReconciler(t, rig, up)
			attachMutationLeaser(r, up)
			got := runReconcile(t, r, up, 1)
			if rig.os.activateCalls != 1 || apimeta.IsStatusConditionTrue(got.Status.Conditions, conditionTypeMutationSettled) != rejected {
				t.Fatalf("activate calls=%d settled=%t rejected=%t", rig.os.activateCalls, apimeta.IsStatusConditionTrue(got.Status.Conditions, conditionTypeMutationSettled), rejected)
			}
			assertMutationLease(t, r, !rejected)
		})
	}
}

func TestInitialActivationControlWaitIsDurableAndBounded(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("initial-activation-timeout", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.RebootTimeoutSeconds = 60
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = up.Spec.TargetVersion
		up.Status.PrimarySupervisorInstallRequested = true
		up.Status.PrimarySupervisorInstalled = true
		up.Status.Conditions = []metav1.Condition{{Type: conditionTypeMutationSettled, Status: metav1.ConditionTrue, Reason: "InstallCompleted"}}
	})
	r := newReconciler(t, rig, up)
	attachMutationLeaser(r, up)
	base := r.now()
	r.GNOI = unavailableGNOI{err: status.Error(codes.Unavailable, "connection unavailable")}
	first := runReconcile(t, r, up, 1)
	if first.Status.ActivationControlStartTime == nil || !first.Status.ActivationControlStartTime.Time.Equal(base) || first.Status.ActivationStartTime != nil {
		t.Fatalf("initial activation clocks: control=%v activation=%v", first.Status.ActivationControlStartTime, first.Status.ActivationStartTime)
	}
	// A replacement controller uses the stored timer; it cannot renew the wait.
	restarted := *r
	restarted.Now = func() time.Time { return base.Add(61 * time.Second) }
	got := runReconcile(t, &restarted, up, 1)
	if got.Status.Phase != opsv1alpha1.UpgradePhaseFailed || got.Status.FailureReason != "ActivationControlTimeout" || rig.os.activateCalls != 0 {
		t.Fatalf("phase=%s reason=%s activate calls=%d", got.Status.Phase, got.Status.FailureReason, rig.os.activateCalls)
	}
	assertMutationLease(t, r, false)
}

func TestInitialActivationRecoveryPreservesFullRebootBudget(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("initial-activation-recovery", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.RebootTimeoutSeconds = 60
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = up.Spec.TargetVersion
	})
	r := newReconciler(t, rig, up)
	base := r.now()
	r.GNOI = unavailableGNOI{err: status.Error(codes.Unavailable, "connection unavailable")}
	_ = runReconcile(t, r, up, 1)
	r.Now = func() time.Time { return base.Add(30 * time.Second) }
	r.GNOI = &staticGNOI{c: rig.client}
	got := runReconcile(t, r, up, 1)
	if got.Status.ActivationStartTime == nil || !got.Status.ActivationStartTime.Time.Equal(r.now()) || rig.os.activateCalls != 1 {
		t.Fatalf("activation start=%v activate calls=%d", got.Status.ActivationStartTime, rig.os.activateCalls)
	}
	if rig.os.activateRemaining < 59*time.Second || rig.os.activateRemaining > time.Minute {
		t.Fatalf("activation RPC budget=%s, want fresh 60s", rig.os.activateRemaining)
	}
}

func TestExpiredActivationWindowSkipsClientAcquisition(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("activation-window-expired", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.MaintenanceWindow = &opsv1alpha1.UpgradeWindow{NotAfter: &metav1.Time{Time: time.Unix(1_699_999_999, 0)}}
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = up.Spec.TargetVersion
	})
	r := newReconciler(t, rig, up)
	provider := &staticGNOI{c: rig.client}
	r.GNOI = provider
	got := runReconcile(t, r, up, 1)
	if got.Status.FailureReason != "MaintenanceWindowExpired" || provider.calls.Load() != 0 {
		t.Fatalf("reason=%s client acquisitions=%d", got.Status.FailureReason, provider.calls.Load())
	}
}

func TestMaintenancePreparationFailurePreventsDispatch(t *testing.T) {
	rig := newRig(t)
	up := newUpgrade("maintenance-preparation", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		up.Status.ValidatedVersion = up.Spec.TargetVersion
	})
	r := newReconciler(t, rig, up)
	attachMutationLeaser(r, up)
	r.BeforeMutation = func(context.Context) error {
		assertMutationLease(t, r, true)
		return errors.New("maintenance taint could not be applied")
	}
	got := runReconcile(t, r, up, 1)
	if conditionReason(got.Status.Conditions, "Ready") != "MutationPreparationBlocked" || rig.os.activateCalls != 0 || got.Status.PrimarySupervisorActivationRequested {
		t.Fatalf("preparation failure authorized mutation: %+v", got.Status)
	}
}
