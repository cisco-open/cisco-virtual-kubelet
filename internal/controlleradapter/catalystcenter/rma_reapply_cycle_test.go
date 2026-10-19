// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

func qualifyRMAReapply(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, raw string, records map[string]deviceReplacementRecord, evaluate func() string, effects func() (int, int)) {
	t.Helper()
	ctx := context.Background()
	oldCleanup := journalState(t, a).ActionApplications[0].Operation.ID
	config.Spec.Source.Inline.Raw = []byte(raw)
	config.Generation++ // Fake client does not implement apiserver generation increments.
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if reason := evaluate(); reason != "ObservationOrPlanFailed" || journalState(t, a).Operation != nil {
		t.Fatal("unqualified retained NON-FAULTY record re-marked", reason)
	}
	delete(records, "old-id")
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name, err := contract.ReservationName(string(nc.UID), oldCleanup)
	if err != nil {
		t.Fatal(err)
	}
	retained := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: nc.Namespace, UID: "still-retained"}}
	if err := a.statusWriter.Create(ctx, retained); err != nil {
		t.Fatal(err)
	}
	if reason := evaluate(); reason != "ObservationOrPlanFailed" || len(journalState(t, a).ActionApplications) != 1 {
		t.Fatal("cleanup receipt lost before reservation retirement", reason)
	}
	if err := a.statusWriter.Delete(ctx, retained); err != nil {
		t.Fatal(err)
	}
	if reason := evaluate(); reason != "ApplyProgressing" || len(journalState(t, a).ActionApplications) != 0 || journalState(t, a).Operation != nil {
		t.Fatal("cleanup receipt retirement dispatched new mark", reason)
	}
	if marks, unmarks := effects(); marks != 1 || unmarks != 1 {
		t.Fatal("bookkeeping performed native mutation")
	}
	run := func(action string) {
		t.Helper()
		reason := evaluate()
		state := journalState(t, a)
		if state.Operation == nil || state.Operation.Phase != "Prepared" || state.Operation.Action != action {
			t.Fatal("new cycle preparation", action, reason)
		}
		op := state.Operation.DeepCopy()
		source, err := loadControllerSource(ctx, a.statusReader, config)
		if err != nil {
			t.Fatal(err)
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := a.compileOwnedInventoryApply(nc, config, source)
		if err != nil {
			t.Fatal(err)
		}
		write, err := plan.executor.next(ctx)
		if err != nil || write == nil {
			t.Fatal("new cycle resume", err)
		}
		row, _ := settledConfigurationFixture(t, a)
		row.Status.ManagerSettledAt = nil
		row.Status.Grant.Phase = "Held"
		row.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: row.Spec.OperationDigest, ClaimedAt: row.Status.Grant.GrantedAt}
		if err := a.statusWriter.Status().Update(ctx, row); err != nil {
			t.Fatal(err)
		}
		if err := a.submitPreparedConfigurationReceipt(ctx, config, source, op, "inventory", write.resource, write.action, func(ctx context.Context) (configurationReceipt, error) {
			task, err := write.submit(ctx)
			return configurationReceipt{task: task}, err
		}, journalTestAdmission, write.identity); err != nil {
			t.Fatal(err)
		}
		evaluate()
		if journalState(t, a).Operation.Phase != "Verifying" {
			t.Fatal("new cycle observation")
		}
		evaluate()
		if journalState(t, a).Operation.Phase != "Verified" {
			t.Fatal("new cycle verification")
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
		if journalState(t, a).Operation != nil {
			t.Fatal("new cycle settlement")
		}
	}
	run("Create")
	if records["old-id"].ID != "record-old-id-2" {
		t.Fatal("new native record was not independently verified")
	}
	var model map[string]any
	if err := json.Unmarshal([]byte(raw), &model); err != nil {
		t.Fatal(err)
	}
	delete(model["catalyst_center"].(map[string]any)["inventory"].(map[string]any)["devices"].([]any)[0].(map[string]any), "rma")
	config.Spec.Source.Inline.Raw, err = json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	config.Generation++
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	run("Delete")
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
			t.Fatal("second cycle finalization", reason)
		}
	}
	state := journalState(t, a)
	if len(state.ActionApplications) != 1 || state.ActionApplications[0].Operation.ID == oldCleanup {
		t.Fatal("second cleanup reused old completion")
	}
	if marks, unmarks := effects(); marks != 2 || unmarks != 2 {
		t.Fatal("second cycle repeated or omitted native operations")
	}
}
