// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

// Runs after the shared native fixture has publicly marked and synthetically
// settled both targets. All manager/worker claims here remain fake actor state.
func qualifyRMAMultiCleanup(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, targets []Device, records map[string]deviceReplacementRecord, evaluate func() string, effects func() int, mode string) {
	t.Helper()
	ctx := context.Background()
	if mode == "prune-multi-foreign" {
		record := records[targets[1].ID]
		record.ID = "foreign"
		records[targets[1].ID] = record
		if reason := evaluate(); reason != "ObservationOrPlanFailed" || effects() != 0 || journalState(t, a).Operation != nil {
			t.Fatal("second target ownership did not block first cleanup", reason)
		}
		return
	}
	finalReceiptCount := len(targets)
	initialMarks := len(targets)
	if mode == "prune-multi-mixed-add" {
		initialMarks--
	}
	if mode == "prune-multi-mixed-retain" {
		targets = targets[:1]
	}
	expectedCleanups := 0
	for index, target := range targets {
		action := "Delete"
		if mode == "prune-multi-mixed-add" && target.ID == "fresh-id" {
			action = "Create"
		} else {
			expectedCleanups++
		}
		reason := evaluate()
		state := journalState(t, a)
		if state.Operation == nil || state.Operation.Phase != "Prepared" || state.Operation.Action != action || state.Operation.TargetID != target.ID {
			t.Fatal("incorrect multi cleanup preparation", reason)
		}
		op := state.Operation.DeepCopy()
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := a.compileOwnedInventoryApply(nc, config, source)
		if err != nil {
			t.Fatal(err)
		}
		write, err := plan.executor.next(ctx)
		if err != nil || write == nil || write.resource != op.Resource {
			t.Fatal("multi cleanup resume", err)
		}
		row, _ := settledConfigurationFixture(t, a)
		row.Status.ManagerSettledAt = nil
		row.Status.Grant.Phase = "Held"
		row.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: row.Spec.OperationDigest, ClaimedAt: row.Status.Grant.GrantedAt}
		if err := a.statusWriter.Status().Update(ctx, row); err != nil {
			t.Fatal(err)
		}
		if mode == "prune-multi-late" && index == 0 {
			record := records[targets[1].ID]
			record.ID = "late-foreign"
			records[targets[1].ID] = record
		}
		err = a.submitPreparedConfigurationReceipt(ctx, config, source, op, "inventory", write.resource, write.action, func(ctx context.Context) (configurationReceipt, error) {
			task, err := write.submit(ctx)
			return configurationReceipt{task: task}, err
		}, journalTestAdmission, write.identity)
		if mode == "prune-multi-late" && index == 0 {
			if err == nil || effects() != 0 {
				t.Fatal("late second-target change allowed native cleanup", err)
			}
			for range 2 {
				evaluate()
			}
			if effects() != 0 || len(journalState(t, a).ActionApplications) != 2 {
				t.Fatal("late-change refusal lost ownership")
			}
			return
		}
		if mode == "prune-multi-lost" && index == 1 {
			if err == nil || journalState(t, a).Operation.Phase != "Unknown" {
				t.Fatal("lost second receipt not fenced", err)
			}
			for range 3 {
				evaluate()
			}
			if effects() != 2 || journalState(t, a).Operation.Phase != "Unknown" || len(journalState(t, a).ActionApplications) != 3 {
				t.Fatal("partial cleanup replayed or retired")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		reason = evaluate()
		if mode == "prune-multi-failed" && index == 1 {
			if reason != "ControllerTaskFailed" || journalState(t, a).Operation.Phase != "Accepted" {
				t.Fatal("failed second task advanced", reason)
			}
			for range 3 {
				evaluate()
			}
			if effects() != 2 || len(journalState(t, a).ActionApplications) != 3 {
				t.Fatal("failed cleanup replayed or retired")
			}
			return
		}
		if journalState(t, a).Operation.Phase != "Verifying" {
			t.Fatal("multi cleanup observation", reason)
		}
		reason = evaluate()
		if journalState(t, a).Operation.Phase != "Verified" {
			t.Fatal("multi cleanup verification", reason)
		}
		nc, err = a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		row.Status.Completion, err = contract.CompletionForReceipt(row, nc, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		row.Status.Grant.Phase = "Released"
		row.Status.ManagerSettledAt = &metav1.Time{Time: time.Now()}
		if err := a.statusWriter.Status().Update(ctx, row); err != nil {
			t.Fatal(err)
		}
		evaluate()
		if journalState(t, a).Operation != nil || effects() != expectedCleanups {
			t.Fatal("multi cleanup settlement or effect count")
		}
	}
	if reason := evaluate(); reason != "ApplyFinalizationPending" || len(journalState(t, a).ActionApplications) != initialMarks+len(targets) {
		t.Fatal("multi cleanup evidence retired early", reason)
	}
	var rows ops.ControllerConfigurationReservationList
	if err := a.statusWriter.List(ctx, &rows, ctrlclient.InNamespace(config.Namespace)); err != nil {
		t.Fatal(err)
	}
	for i := range rows.Items {
		if err := contract.RetireSettledReservation(ctx, a.statusReader, a.statusWriter, &rows.Items[i], time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		if reason := evaluate(); reason != "ApplyVerified" {
			t.Fatal("multi cleanup finalization", reason)
		}
	}
	if effects() != expectedCleanups || len(journalState(t, a).ActionApplications) != finalReceiptCount {
		t.Fatal("multi cleanup did not retain only cleanup tombstones")
	}
	if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("multi cleanup not InSync")
	}
}
