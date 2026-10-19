// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var discoveryDeletionDrainModes = []string{"create", "known-task", "prepared", "delete", "prune", "missing-task", "submitting", "failed-task", "wrong-task", "generation", "owner", "source-version", "foreign-resource", "native-drift", "submitted-later", "missing-receipt"}

func TestDiscoveryConfigDeletionDrain(t *testing.T) {
	for _, mode := range discoveryDeletionDrainModes {
		t.Run(mode, func(t *testing.T) { exerciseDiscoveryDeletionDrain(t, mode, nil) })
	}
}

func exerciseDiscoveryDeletionDrain(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, c, world := discoveryPruneFixture(t)
	if install != nil {
		install(a, c)
	}
	c.Spec.Source.Inline.Raw = discoveryModel()
	c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	c.Finalizers = []string{"test.cisco.vk/drain-only"}
	if err := a.statusWriter.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	op := nc.Status.Configuration.ActionApplications[0].Operation.DeepCopy()
	op.ConfigGeneration = c.Generation
	op.Phase = "Accepted"
	op.TaskID = "drain-task"
	stamp := metav1.NewTime(time.Now().Add(-time.Second))
	op.SubmittedAt = &stamp
	data, _ := json.Marshal(map[string]string{"discoveryId": "42"})
	world.tasks[op.TaskID] = map[string]any{"id": op.TaskID, "startTime": stamp.UnixMilli(), "endTime": time.Now().UnixMilli(), "isError": false, "data": string(data)}
	if mode == "delete" || mode == "prune" {
		op.Action = "Delete"
		op.TargetID = "42"
		if mode == "prune" {
			op.Resource += discoveryPruneSuffix
		}
		delete(world.jobs, "fixture")
	} else {
		nc.Status.Configuration.ActionApplications = nil
	}
	switch mode {
	case "known-task":
		op.Phase = "Unknown"
	case "prepared":
		op.Phase = "Prepared"
		op.TaskID = ""
		op.SubmittedAt = nil
	case "missing-task":
		op.Phase = "Unknown"
		op.TaskID = ""
	case "submitting":
		op.Phase = "Submitting"
		op.TaskID = ""
	case "failed-task":
		world.tasks[op.TaskID]["isError"] = true
	case "wrong-task":
		world.tasks[op.TaskID]["id"] = "foreign"
	case "owner":
		nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
	case "source-version":
		op.SourceUID = "changed"
		op.SourceVersion = "changed"
	case "foreign-resource":
		op.Resource = "unrelated:metadata"
	case "generation":
		op.ConfigGeneration = c.Generation + 2
	case "native-drift":
		row := world.jobs["fixture"]
		row.Protocol = "TELNET"
		world.jobs["fixture"] = row
	case "submitted-later":
		future := metav1.NewTime(time.Now().Add(time.Minute))
		op.SubmittedAt = &future
	}
	nc.Status.Configuration.Operation = op
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := a.statusWriter.Delete(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	// Before draining, ordinary submission snapshots remain forbidden.
	source, err := loadControllerSource(ctx, a.statusReader, c)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "prepared" {
		source.discoveryDrain = &discoveryConfigDrainContext{ConfigUID: string(c.UID), SpecDigest: featureIdentityDigest(c.Spec), DeletionDigest: featureIdentityDigest(c.DeletionTimestamp), OperationID: op.ID, OperationGeneration: op.ConfigGeneration}
		if !discoveryOperationGenerationMatches(c, source, op) {
			t.Fatal("observation generation bridge refused")
		}
		other := op.DeepCopy()
		other.ID = strings.Repeat("f", 32)
		if discoveryOperationGenerationMatches(c, source, other) {
			t.Fatal("observation context transferred to another operation")
		}
		if _, err := a.prepareConfiguration(ctx, c, source, "inventory", op.Resource, op.Action); err == nil {
			t.Fatal("drain context prepared a native mutation")
		}
	}
	if err := a.startConfiguration(ctx, c, source, op.ID); err == nil {
		t.Fatal("deletion authorized another submission")
	}
	err = a.drainDiscoveryConfigOperation(ctx, c)
	blocked := mode == "missing-task" || mode == "submitting" || mode == "failed-task" || mode == "wrong-task" || mode == "owner" || mode == "source-version" || mode == "foreign-resource" || mode == "generation" || mode == "submitted-later"
	if blocked {
		if err == nil || journalState(t, a).Operation == nil || len(world.writes) != 0 {
			t.Fatal("unsafe drain progressed", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if mode == "prepared" {
		if journalState(t, a).Operation != nil || len(world.writes) != 0 {
			t.Fatal("Prepared cancellation mutated native state")
		}
		return
	}
	// Reload the adapter object at every boundary; no closure supplies authority.
	restarted := *a
	a = &restarted
	if journalState(t, a).Operation.Phase != "Verifying" {
		t.Fatal("original task was not observed")
	}
	err = a.drainDiscoveryConfigOperation(ctx, c)
	if mode == "native-drift" {
		if err == nil || journalState(t, a).Operation.Phase != "Verifying" || len(world.writes) != 0 {
			t.Fatal("drift was recorded as ownership")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	state := journalState(t, a)
	if state.Operation.Phase != "Verified" || !ownedDiscoveryReceiptPresent(state, state.Operation) || state.ActionApplications[0].Operation.ConfigGeneration != op.ConfigGeneration {
		t.Fatal("original generation receipt was lost")
	}
	if mode == "missing-receipt" {
		nc, _ := a.currentController(ctx)
		nc.Status.Configuration.ActionApplications = nil
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
		if err := a.drainDiscoveryConfigOperation(ctx, c); err == nil {
			t.Fatal("receiptless drain settled")
		}
		return
	}
	if err := a.drainDiscoveryConfigOperation(ctx, c); err != nil {
		t.Fatal(err)
	}
	state = journalState(t, a)
	if state.Operation != nil || len(state.Owners) != 1 || len(state.ActionApplications) != 1 || len(world.writes) != 0 {
		t.Fatal("drain lost owner/receipt or invoked a writer")
	}
	if !strings.HasPrefix(state.ActionApplications[0].Resource, ownedDiscoveryReceiptPrefix) {
		t.Fatal("drain invented a parallel receipt")
	}
	if mode == "create" || mode == "known-task" {
		// The newly retained original Create receipt authorizes the independent
		// deletion-bound lifecycle; the drain itself made no native request.
		if _, err := a.prepareDiscoveryConfigCleanup(ctx, c); err != nil {
			t.Fatal("drained receipt could not hand off to cleanup", err)
		}
	}
}
