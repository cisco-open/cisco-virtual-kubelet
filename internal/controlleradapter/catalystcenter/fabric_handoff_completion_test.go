// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	coordination "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Continue the native submission fixture through the actual terminal adapter
// route. Worker release/manager settlement are simulated status evidence here;
// this does not qualify installed actors or permit the adapter to release leases.
func testFabricHandoffCompletion(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, keepSource ...bool) {
	t.Helper()
	ctx := context.Background()
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	members, err := contract.ReadVerifiedGroupReservations(ctx, a.statusReader, nc, time.Now())
	if err != nil || len(members) == 0 {
		t.Fatal("verified members", err)
	}
	var leases coordination.LeaseList
	if err := a.statusReader.List(ctx, &leases); err != nil {
		t.Fatal(err)
	}
	retained := nc.Status.Configuration.DeepCopy()
	retained.Operation = nil
	a.workerPodName, a.workerPodUID = members[0].Spec.ControllerWorkerPod.Name, members[0].Spec.ControllerWorkerPod.UID
	denied := true
	if err := a.ConfigureConfigurationReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
		if denied {
			return errors.New("fixture denied terminal actor")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.completeConfigurationGroup(ctx, nc); err == nil {
		t.Fatal("unauthorized completion succeeded")
	}
	for _, member := range members {
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(member), member); err != nil {
			t.Fatal(err)
		}
		if member.Status.Completion != nil {
			t.Fatal("denied actor published completion")
		}
	}
	denied = false
	// Retain source only for the composed InSync check. Other callers still
	// prove terminal recovery after source deletion.
	if len(keepSource) == 0 || !keepSource[0] {
		if err := a.statusWriter.Delete(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	r := &configReconciler{a: a}
	// A partial completion write must recover without replaying native work.
	writer := a.statusWriter
	a.statusWriter = lostJournalAckClient{writer}
	if handled, err := r.recoverTerminalConfiguration(ctx, config.Name); !handled || err == nil {
		t.Fatal("lost completion acknowledgment not retained", err)
	}
	a.statusWriter = writer
	completed := 0
	for _, member := range members {
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(member), member); err != nil {
			t.Fatal(err)
		}
		if member.Status.Completion != nil {
			completed++
		}
	}
	if completed != 1 {
		t.Fatal("fixture did not retain first completion", completed)
	}
	for i := 0; i < 2; i++ {
		if handled, err := r.recoverTerminalConfiguration(ctx, config.Name); !handled || err == nil {
			t.Fatal("held group cleared", err)
		}
		for _, member := range members {
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(member), member); err != nil {
				t.Fatal(err)
			}
			if member.Status.Completion == nil || member.Status.Grant.Phase != "Held" || member.Status.ManagerSettledAt != nil {
				t.Fatal("completion changed release evidence")
			}
		}
		expected, err := contract.GroupCompletionsForReceipt(nc, members, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		for _, member := range members {
			if !reflect.DeepEqual(expected[member.UID], member.Status.Completion) {
				t.Fatal("completion not bound to receipt")
			}
		}
	}
	// Neither partial release nor complete release without manager settlement
	// may clear the retained journal.
	for _, member := range members {
		member.Status.Grant.Phase = "Released"
		if err := a.statusWriter.Status().Update(ctx, member); err != nil {
			t.Fatal(err)
		}
		if handled, err := r.recoverTerminalConfiguration(ctx, config.Name); !handled || err == nil {
			t.Fatal("unsettled group cleared", err)
		}
	}
	for i, member := range members {
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(member), member); err != nil {
			t.Fatal(err)
		}
		at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
		member.Status.ManagerSettledAt = &at
		if err := a.statusWriter.Status().Update(ctx, member); err != nil {
			t.Fatal(err)
		}
		handled, err := r.recoverTerminalConfiguration(ctx, config.Name)
		if !handled || (err == nil) != (i == len(members)-1) {
			t.Fatal("incorrect all-member settlement result", i, err)
		}
	}

	fresh, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fresh.Status.Configuration, retained) {
		t.Fatal("terminal recovery lost retained ownership or kept journal")
	}
	if handled, err := r.recoverTerminalConfiguration(ctx, config.Name); handled || err != nil {
		t.Fatal("cleared operation recovered twice", err)
	}
	var after coordination.LeaseList
	if err := a.statusReader.List(ctx, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(leases.Items, after.Items) {
		t.Fatal("controller completion mutated device leases")
	}
}
