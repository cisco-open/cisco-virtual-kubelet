// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

func testPublicHandoffRecovery(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, members []*ops.ControllerConfigurationReservation, mode string) {
	t.Helper()
	ctx := context.Background()
	restarted := *a

	restarted.workerPodName, restarted.workerPodUID = members[0].Spec.ControllerWorkerPod.Name, members[0].Spec.ControllerWorkerPod.UID
	if err := restarted.ConfigureConfigurationReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
		if mode == "dispatch-recovery-public-denied" {
			return errors.New("fixture denied public recovery")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if mode == "dispatch-recovery-public-source" {
		config.Generation++
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	successful := mode == "dispatch-recovery-public" || mode == "dispatch-recovery-public-unknown"
	if mode == "dispatch-recovery-public-unknown" {
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		nc.Status.Configuration.Operation.Phase = "Unknown"
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "dispatch-recovery-public-replaced" {
		restarted.workerPodUID = "replacement-pod-uid"
	}
	if mode == "dispatch-recovery-public-missing" {
		if err := a.statusWriter.Delete(ctx, members[0]); err != nil {
			t.Fatal(err)
		}
	}
	for step := 0; step < 2; step++ {
		result, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)})
		if err != nil {
			t.Fatal("public reconcile", err)
		}
		if result.RequeueAfter != 5*time.Second {
			t.Fatalf("recovery requeue=%v", result.RequeueAfter)
		}
		var fresh configapi.NetworkControllerConfig
		if err := restarted.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &fresh); err != nil {
			t.Fatal(err)
		}
		status := &fresh.Status
		condition := meta.FindStatusCondition(status.Conditions, configapi.NetworkControllerConfigConditionIntentValid)
		if condition == nil {
			t.Fatal("missing intent condition")
		}
		reason := condition.Reason

		expectedReason := "ApplyProgressing"
		expectedPhase := "Verifying"
		if step == 1 {
			expectedPhase = "Verified"
		}
		if !successful {
			expectedReason, expectedPhase = "GroupVerificationPending", "Accepted"
		}
		if reason != expectedReason || status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
			t.Fatalf("public recovery: reason=%s phase=%s", reason, status.Phase)
		}
		if len(status.SectionStatus) != 1 || status.SectionStatus[0].Name != "fabric" || status.SectionStatus[0].State != configapi.NetworkControllerSectionStateApplying {
			t.Fatalf("recovery section status=%+v", status.SectionStatus)
		}
		state := journalState(t, a)
		if state.Operation.Phase != expectedPhase {
			t.Fatalf("public recovery journal phase=%s", state.Operation.Phase)
		}
		if (len(state.ActionApplications) == 1) != (expectedPhase == "Verified") {
			t.Fatal("public recovery receipt mismatch")
		}
	}
	if successful {
		// The terminal fixture installs its own denial/release actor.
		restarted.configurationAdmission = nil
		testFabricHandoffCompletion(t, &restarted, config, true)
		restarted.configurationAdmission = nil
		if err := restarted.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
			t.Fatal("converged readback requested a mutation permit")
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for pass := 0; pass < 2; pass++ {
			result, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)})
			if err != nil || result.RequeueAfter != 5*time.Minute {
				t.Fatalf("converged reconcile=%+v error=%v", result, err)
			}
			var fresh configapi.NetworkControllerConfig
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &fresh); err != nil {
				t.Fatal(err)
			}
			ready := meta.FindStatusCondition(fresh.Status.Conditions, configapi.NetworkControllerConfigConditionReady)
			if fresh.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || ready == nil || string(ready.Status) != "True" || len(fresh.Status.SectionStatus) != 1 || fresh.Status.SectionStatus[0].State != configapi.NetworkControllerSectionStateInSync {
				t.Fatalf("converged status=%+v", fresh.Status)
			}
			state := journalState(t, a)
			if state.Operation != nil || len(state.ActionApplications) != 1 {
				t.Fatal("converged readback changed the retained receipt")
			}
		}
	}

}
