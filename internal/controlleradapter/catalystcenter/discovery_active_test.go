// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestDiscoveryActiveOwnershipReplacementAndPruning(t *testing.T) {
	a, config, world := discoveryPruneFixture(t)
	exerciseDiscoveryActiveLifecycle(t, a, config, world)
}
func exerciseDiscoveryActiveLifecycle(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, world *discoveryPruneWorld) {
	t.Helper()
	ctx := context.Background()
	world.createActive = true
	delete(world.jobs, "fixture")
	config.Generation = 1
	config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
	config.Spec.Source.Inline.Raw = discoveryModel()
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	nc, _ := a.currentController(ctx)
	nc.Status.Configuration.ActionApplications = nil
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	recovered := map[string]bool{}
	tick := func() {
		t.Helper()
		restart := *a
		if _, err := (&configReconciler{a: &restart}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if op := nc.Status.Configuration.Operation; op != nil && op.Phase == "Accepted" && !recovered[op.ID] {
			recovered[op.ID] = true
			op.Phase = "Unknown"
			raw, _ := json.Marshal(nc.Status.Configuration)
			_ = json.Unmarshal(raw, &nc.Status.Configuration)
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
		}
	}
	ownActive := func() {
		t.Helper()
		for range 50 {
			tick()
			state := journalState(t, a)
			if state.Operation == nil && len(state.ActionApplications) == 1 && state.ActionApplications[0].Operation.Action == "Create" && state.ActionApplications[0].Operation.ConfigGeneration == config.Generation {
				break
			}
		}
		state := journalState(t, a)
		if state.Operation != nil || len(state.ActionApplications) != 1 || !validOwnedDiscoveryReceipt(state.ActionApplications[0]) || state.ActionApplications[0].Operation.ConfigGeneration != config.Generation || world.jobs["fixture"].Status != "Active" || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("active creation was unowned or falsely InSync", config.Status.Conditions)
		}
		for range 3 {
			tick()
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("active job declared InSync after ownership")
		}
	}
	ownActive()
	if len(world.writes) != 1 || len(recovered) != 1 {
		t.Fatal("creation replayed", world.writes)
	}
	config.Generation++
	config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(discoveryModel()), `"SSH"`, `"SSH,TELNET"`, 1))
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	ownActive()
	state := journalState(t, a)
	if len(world.writes) != 3 || len(recovered) != 3 || state.ActionApplications[0].Operation.Action != "Create" || world.jobs["fixture"].Protocol != "SSH,TELNET" {
		t.Fatal("active replacement did not converge", world.writes, config.Status.Conditions)
	}
	config.Generation++
	config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	config.Spec.Source.Inline.Raw = []byte(emptyDiscoveryModel)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		tick()
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
			break
		}
	}
	state = journalState(t, a)
	if len(world.writes) != 4 || len(recovered) != 4 || len(world.jobs) != 1 || world.jobs["foreign"].ID != "foreign-id" || state.Operation != nil || state.ActionApplications[0].Operation.Action != "Delete" || !strings.HasSuffix(state.ActionApplications[0].Operation.Resource, discoveryPruneSuffix) || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("active prune failed", world.writes, config.Status.Conditions)
	}
	for range 3 {
		tick()
	}
	if len(world.writes) != 4 {
		t.Fatal("active deletion replayed")
	}
}

func TestDiscoveryActiveCreationStillRequiresNativeFingerprint(t *testing.T) {
	ctx := context.Background()
	a, config, world := discoveryPruneFixture(t)
	world.createActive = true
	delete(world.jobs, "fixture")
	config.Spec.Source.Inline.Raw = discoveryModel()
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	nc, _ := a.currentController(ctx)
	nc.Status.Configuration.ActionApplications = nil
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	changed := false
	for range 30 {
		restart := *a
		if _, err := (&configReconciler{a: &restart}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
		state := journalState(t, a)
		if state.Operation != nil && state.Operation.Phase == "Accepted" && !changed {
			row := world.jobs["fixture"]
			row.Protocol = "TELNET"
			world.jobs["fixture"] = row
			changed = true
		}
	}
	state := journalState(t, a)
	if !changed || len(world.writes) != 1 || state.Operation == nil || len(state.ActionApplications) != 0 {
		t.Fatal("changed active footprint acquired ownership or replayed", world.writes)
	}
	// A new source cannot make an unverified native create into an owned stop/delete.
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	config.Generation++
	config.Spec.Source.Inline.Raw = []byte(emptyDiscoveryModel)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		restart := *a
		if _, err := (&configReconciler{a: &restart}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(world.writes) != 1 || journalState(t, a).Operation == nil {
		t.Fatal("unverified creation was removed through source change")
	}
}
