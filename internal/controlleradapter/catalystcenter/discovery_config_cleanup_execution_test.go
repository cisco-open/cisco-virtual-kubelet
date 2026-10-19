// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"net/http"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var discoveryCleanupExecutionModes = []string{"normal", "known-task", "lost-ack", "backend-error", "native-drift", "receipt-race", "spec-race", "dispatch-owner-race", "dispatch-native-race", "task-running", "task-failed", "task-mismatch", "absence-pending", "alias", "receipt-missing", "malformed-envelope"}

func TestDiscoveryConfigCleanupExecution(t *testing.T) {
	for _, mode := range discoveryCleanupExecutionModes {
		t.Run(mode, func(t *testing.T) { exerciseDiscoveryCleanupExecution(t, mode, nil) })
	}
}

func exerciseDiscoveryCleanupExecution(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, c, world := discoveryPruneFixture(t)
	if install != nil {
		install(a, c)
	}
	c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	c.Finalizers = []string{"test.cisco.vk/execution-only"}
	if err := a.statusWriter.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := a.statusWriter.Delete(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	op, err := a.prepareDiscoveryConfigCleanup(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if !ownedDiscoveryOperation(op) {
		t.Fatal("finalization record not recognized as owned")
	}
	original := world.jobs["fixture"]
	switch mode {
	case "lost-ack", "backend-error":
		world.mode = mode
	case "native-drift":
		row := original
		row.Protocol = "TELNET"
		world.jobs["fixture"] = row
	case "receipt-race":
		nc, _ := a.currentController(ctx)
		nc.Status.Configuration.ActionApplications = nil
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	case "spec-race":
		c.Spec.ModelSource.Exporter = "changed"
		if err := a.statusWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
	case "malformed-envelope":
		nc, _ := a.currentController(ctx)
		nc.Status.Configuration.Operation.Resource = discoveryFinalizePrefix + "malformed"
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "dispatch-owner-race" || mode == "dispatch-native-race" {
		rest, _, err := a.client.session(ctx)
		if err != nil {
			t.Fatal(err)
		}
		base := rest.HTTPClient.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		injected := false
		rest.HTTPClient.Transport = discoveryCleanupRoundTrip(func(req *http.Request) (*http.Response, error) {
			if !injected && req.Method == "GET" && req.URL.Path == discoveryPath+"/1/100" {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if nc.Status.Configuration.Operation.Phase == "Submitting" {
					injected = true
					if mode == "dispatch-owner-race" {
						nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
						if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
							t.Fatal(err)
						}
					} else {
						row := world.jobs["fixture"]
						row.Protocol = "TELNET"
						world.jobs["fixture"] = row
					}
				}
			}
			return base.RoundTrip(req)
		})
	}
	err = a.advanceDiscoveryConfigCleanup(ctx, c)
	if mode == "native-drift" || mode == "receipt-race" || mode == "spec-race" || mode == "malformed-envelope" || strings.HasPrefix(mode, "dispatch-") {
		if err == nil || len(world.writes) != 0 {
			t.Fatal("invalid preparation dispatched", err, world.writes)
		}
		if mode == "malformed-envelope" {
			if err := a.advanceConfiguration(ctx, op.ID, "Verified", ""); err == nil {
				t.Fatal("malformed envelope generically verified")
			}
		}
		if strings.HasPrefix(mode, "dispatch-") && journalState(t, a).Operation.Phase != "Unknown" {
			t.Fatal("post-permit refusal not retained")
		}
		return
	}
	if mode == "lost-ack" || mode == "backend-error" {
		if err == nil || journalState(t, a).Operation.Phase != "Unknown" {
			t.Fatal("uncertain submission not fenced", err)
		}
		for range 3 {
			if err := a.advanceDiscoveryConfigCleanup(ctx, c); err == nil {
				t.Fatal("missing-task uncertainty advanced")
			}
		}
		if len(world.writes) != 1 {
			t.Fatal("uncertain delete replayed", world.writes)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	accepted := journalState(t, a).Operation.DeepCopy()
	if accepted.Phase != "Accepted" || accepted.TaskID != "task-1" || len(world.writes) != 1 {
		t.Fatal("delete not accepted exactly once", accepted, world.writes)
	}
	// Reconstruct adapter-local state from only the persisted journal.
	restarted := *a
	a = &restarted
	if mode == "known-task" {
		if err := a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
			t.Fatal(err)
		}
	}
	switch mode {
	case "task-running":
		delete(world.tasks["task-1"], "endTime")
	case "task-failed":
		world.tasks["task-1"]["isError"] = true
	case "task-mismatch":
		world.tasks["task-1"]["id"] = "foreign-task"
	}
	err = a.advanceDiscoveryConfigCleanup(ctx, c)
	if strings.HasPrefix(mode, "task-") {
		if err == nil || journalState(t, a).Operation.Phase != "Accepted" || len(world.writes) != 1 {
			t.Fatal("uncompleted task released or replayed", err)
		}
		return
	}
	if err != nil || journalState(t, a).Operation.Phase != "Verifying" {
		t.Fatal("original task not recovered", err)
	}
	if err := a.advanceConfiguration(ctx, op.ID, "Verified", ""); err == nil {
		t.Fatal("generic verification bypassed receipt")
	}
	if mode == "absence-pending" {
		world.jobs["fixture"] = original
	}
	if mode == "alias" {
		original.ID = "foreign-alias"
		world.jobs["fixture"] = original
	}
	err = a.advanceDiscoveryConfigCleanup(ctx, c)
	if mode == "absence-pending" || mode == "alias" {
		if err == nil || journalState(t, a).Operation.Phase != "Verifying" || len(world.writes) != 1 {
			t.Fatal("unverified absence released", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	state := journalState(t, a)
	if state.Operation.Phase != "Verified" || !ownedDiscoveryReceiptPresent(state, state.Operation) || state.ActionApplications[0].Operation.Resource != op.Resource {
		t.Fatal("deletion-bound receipt absent")
	}
	if mode == "receipt-missing" {
		nc, _ := a.currentController(ctx)
		nc.Status.Configuration.ActionApplications = nil
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
		if err := a.finishConfiguration(ctx, op.ID); err == nil {
			t.Fatal("missing receipt allowed generic settlement")
		}
		if err := a.advanceDiscoveryConfigCleanup(ctx, c); err == nil {
			t.Fatal("missing receipt released cleanup")
		}
		return
	}
	if err := a.advanceDiscoveryConfigCleanup(ctx, c); err != nil {
		t.Fatal(err)
	}
	state = journalState(t, a)
	if state.Operation != nil || len(state.Owners) != 1 || len(state.ActionApplications) != 1 || state.ActionApplications[0].Operation.Action != "Delete" || !validOwnedDiscoveryReceipt(state.ActionApplications[0]) || len(world.writes) != 1 {
		t.Fatal("cleanup did not preserve exact tombstone and owner")
	}
	if world.jobs["foreign"].ID != "foreign-id" {
		t.Fatal("foreign discovery changed")
	}
	var fresh configapi.NetworkControllerConfig
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), &fresh); err != nil || len(fresh.Finalizers) != 1 {
		t.Fatal("private execution released public finalizer", err)
	}
	if _, err := a.prepareDiscoveryConfigCleanup(ctx, c); err == nil {
		t.Fatal("empty candidate set treated as more work")
	}
}

// Pruned/invalid envelopes must retain the generic verification/settlement
// fence even though no resource-specific executor can parse them.
func TestDiscoveryConfigCleanupMalformedSettlement(t *testing.T) {
	a, _, _ := discoveryPruneFixture(t)
	ctx := context.Background()
	nc, _ := a.currentController(ctx)
	op := nc.Status.Configuration.ActionApplications[0].Operation.DeepCopy()
	op.Resource = discoveryFinalizePrefix + "pruned"
	op.Action = "Delete"
	op.TargetID = "42"
	op.Phase = "Verifying"
	nc.Status.Configuration.Operation = op
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if !ownedDiscoverySelected(op.Resource) || ownedDiscoveryOperation(op) {
		t.Fatal("malformed selector lost fence")
	}
	if err := a.advanceConfiguration(ctx, op.ID, "Verified", ""); err == nil {
		t.Fatal("malformed operation generically verified")
	}
	nc.Status.Configuration.Operation.Phase = "Verified"
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := a.finishConfiguration(ctx, op.ID); err == nil {
		t.Fatal("malformed Verified operation released")
	}
}

type discoveryCleanupRoundTrip func(*http.Request) (*http.Response, error)

func (f discoveryCleanupRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
