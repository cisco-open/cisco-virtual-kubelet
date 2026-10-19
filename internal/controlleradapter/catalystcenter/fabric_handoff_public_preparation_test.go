// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

func testPublicHandoffPreparation(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig) {
	t.Helper()
	ctx := context.Background()
	a.workerPodName, a.workerPodUID = "connector", "connector-uid"
	if err := a.ConfigureConfigurationReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
		t.Fatal("legacy setup attempted handoff authorization")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
		t.Fatal(err)
	}
	if state := journalState(t, a); state != nil && state.Operation != nil {
		t.Fatal("single-device installation prepared a group")
	}
	a.configurationAdmission = nil
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
		t.Fatal("preparation attempted physical operation authorization")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	defer func() { a.configurationAdmission = nil }()
	var operationID string
	for step := 0; step < 2; step++ {
		result, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)})
		if err != nil || result.RequeueAfter != 5*time.Second {
			t.Fatalf("public preparation result=%+v error=%v", result, err)
		}
		op := journalState(t, a).Operation
		if op == nil || op.Phase != "Prepared" || op.TaskID != "" || op.SubmittedAt != nil || contract.ValidateGroupJournalIdentity(op) != nil {
			t.Fatal("invalid public preparation journal")
		}
		if step == 1 && op.ID != operationID {
			t.Fatal("repeated reconciliation replaced preparation")
		}
		operationID = op.ID
		var members ops.ControllerConfigurationReservationList
		if err := a.statusReader.List(ctx, &members); err != nil {
			t.Fatal(err)
		}
		if len(members.Items) != 0 {
			t.Fatal("connector preparation created reservations")
		}
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
}

func testPublicHandoffDispatch(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig) Task {
	t.Helper()
	ctx := context.Background()
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var list ops.ControllerConfigurationReservationList
	if err := a.statusReader.List(ctx, &list); err != nil {
		t.Fatal(err)
	}
	members := make([]*ops.ControllerConfigurationReservation, len(list.Items))
	for i := range list.Items {
		members[i] = &list.Items[i]
	}
	if err := contract.ReadGroupAuthority(ctx, a.statusReader, nc, members, ops.ConfigurationReservationReference{Name: a.workerPodName, UID: a.workerPodUID}, "leases", time.Now()); err != nil {
		t.Fatal("fixture group authority", err)
	}
	result, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)})
	if err != nil || result.RequeueAfter != 5*time.Second {
		t.Fatalf("dispatch result=%+v error=%v", result, err)
	}
	op := journalState(t, a).Operation
	if op.Phase != "Accepted" || op.TaskID == "" {
		t.Fatalf("public dispatch phase=%s task=%s", op.Phase, op.TaskID)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	return Task{ID: op.TaskID}
}
