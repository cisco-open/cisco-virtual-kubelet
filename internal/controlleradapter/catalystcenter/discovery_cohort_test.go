// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func discoveryCohortModel(names ...string) []byte {
	jobs := []any{}
	for _, name := range names {
		jobs = append(jobs, map[string]any{"name": name, "type": "SINGLE", "ip_address_list": "192.0.2.1", "protocol_order": "SSH", "global_credential_list": []string{"lab-cli"}})
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"discovery": jobs}}})
	return raw
}
func TestDiscoveryCohortProgressWhileRetainedJobActive(t *testing.T) {
	a, config, world := discoveryPruneFixture(t)
	exerciseDiscoveryCohortProgress(t, a, config, world)
}
func exerciseDiscoveryCohortProgress(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, world *discoveryPruneWorld) {
	t.Helper()
	ctx := context.Background()
	world.allowedCreates["second"] = true
	row := world.jobs["fixture"]
	row.Condition = "In Progress"
	row.Status = "Active"
	world.jobs["fixture"] = row
	revise := func(raw []byte) {
		t.Helper()
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		config.Generation++
		config.Spec.Source.Inline.Raw = raw
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	revise(discoveryCohortModel("fixture", "second"))
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
	settled := func(writes int) {
		t.Helper()
		for range 50 {
			tick()
			if len(world.writes) == writes && journalState(t, a).Operation == nil {
				break
			}
		}
		for range 3 {
			tick()
		}
		if len(world.writes) != writes || journalState(t, a).Operation != nil {
			t.Fatal("independent cohort work stalled/replayed", world.writes, config.Status.Conditions)
		}
	}
	settled(1)
	if world.writes[0] != "POST second" || world.jobs["fixture"].Status != "Active" || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || len(journalState(t, a).ActionApplications) != 2 {
		t.Fatal("create blocked by active peer or falsely InSync", world.writes, config.Status.Conditions)
	}
	revise(discoveryCohortModel("fixture"))
	settled(2)
	if _, present := world.jobs["second"]; present || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || world.jobs["fixture"].Status != "Active" {
		t.Fatal("prune blocked by active retained job or falsely InSync", world.writes, config.Status.Conditions)
	}
	row = world.jobs["fixture"]
	row.Condition = "Complete"
	row.Status = "Inactive"
	world.jobs["fixture"] = row
	for range 6 {
		tick()
	}
	if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || len(world.writes) != 2 {
		t.Fatal("completed cohort did not converge without writes", config.Status.Conditions)
	}
	revise([]byte(emptyDiscoveryModel))
	settled(3)
	if len(recovered) != 3 || len(world.jobs) != 1 || world.jobs["foreign"].ID != "foreign-id" || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("final cohort cleanup failed", world.writes, config.Status.Conditions)
	}
}

func TestDiscoveryCohortActivePeerDoesNotMaskDrift(t *testing.T) {
	for _, mode := range []string{"initial-drift", "late-drift", "late-identity"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, world := discoveryPruneFixture(t)
			row := world.jobs["fixture"]
			row.Status = "Active"
			row.Condition = "In Progress"
			world.jobs["fixture"] = row
			second := world.jobs["foreign"]
			second.Name = "second"
			second.ID = "second-id"
			world.jobs["second"] = second
			config.Spec.Source.Inline.Raw = discoveryCohortModel("fixture", "new", "second")
			world.allowedCreates["new"] = true
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			nc, _ := a.currentController(ctx)
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			plan, handled, err := a.compileOwnedDiscoveryApply(nc, config, source)
			if err != nil || !handled {
				t.Fatal(err)
			}
			if mode == "initial-drift" {
				second.Protocol = "TELNET"
				world.jobs["second"] = second
			}
			write, err := plan.executor.next(ctx)
			if mode == "initial-drift" {
				if err == nil || write != nil || len(world.writes) != 0 {
					t.Fatal("active peer hid invalid later member")
				}
				return
			}
			if err != nil || write == nil || write.action != "Create" {
				t.Fatal("independent create not planned", err)
			}
			if mode == "late-drift" {
				second.Protocol = "TELNET"
				world.jobs["second"] = second
			} else {
				row.ID = "replacement"
				world.jobs["fixture"] = row
			}
			if _, err := write.submit(ctx); err == nil || len(world.writes) != 0 {
				t.Fatal("late cohort drift reached dispatch", err)
			}
			if _, err := write.submit(ctx); err == nil || len(world.writes) != 0 {
				t.Fatal("stale plan replayed")
			}
		})
	}
}
