// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"testing"
)

func TestWiredInventorySequencingAndRecovery(t *testing.T) {
	ctx := context.Background()
	executor, _ := newWiredExecutorFixture(t)
	submits := 0
	prerequisite := &wirelessStageFixture{write: &configurationWrite{resource: "assignments/edge", submit: func(context.Context) (Task, error) { submits++; return Task{ID: "task"}, nil }}}
	checks := 0
	valid := true
	stages := &wiredInventoryStages{adapter: executor.adapter, prerequisites: prerequisite, provisioning: []*wiredProvisionExecutor{executor}, validate: func(context.Context) error {
		checks++
		if !valid {
			return errConfigurationStale
		}
		return nil
	}}
	write, err := stages.next(ctx)
	if err != nil || write == nil || write.resource != "assignments/edge" {
		t.Fatal(write, err)
	}
	valid = false
	if _, err := write.submit(ctx); err == nil || submits != 0 {
		t.Fatal("late invalidation submitted prerequisite")
	}
	valid = true
	prerequisite.write = nil
	write, err = stages.next(ctx)
	if err != nil || write == nil || !validWiredProvisionResource(write.resource) {
		t.Fatal(write, err)
	}
	op, err := executor.adapter.prepareConfiguration(ctx, executor.config, nil, "inventory", write.resource, write.action, write.identity)
	if err != nil {
		t.Fatal(err)
	}
	other := *executor
	other.desired.reference.Serial = "other-serial"
	stages.provisioning = []*wiredProvisionExecutor{&other, executor}
	recovered, err := stages.next(ctx)
	if err != nil || recovered == nil || recovered.resource != op.Resource {
		t.Fatal("wrong executor selected", recovered, err)
	}
	prerequisite.write = &configurationWrite{resource: "assignments/changed"}
	if write, err := stages.next(ctx); !errors.Is(err, errConfigurationBusy) || write != nil {
		t.Fatal("prepared provision replaced by prerequisite", write, err)
	}
	prerequisite.write = nil
	if err := executor.adapter.startConfiguration(ctx, executor.config, nil, op.ID, recovered.identity); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"Accepted", "Verifying"} {
		task := ""
		if phase == "Accepted" {
			task = "task"
		}
		if err := executor.adapter.advanceConfiguration(ctx, op.ID, phase, task); err != nil {
			t.Fatal(err)
		}
	}
	verifying := journalState(t, executor.adapter).Operation.DeepCopy()
	stages.provisioning = []*wiredProvisionExecutor{executor, executor}
	if ok, err := stages.verify(ctx, verifying); ok || !errors.Is(err, errConfigurationOwned) {
		t.Fatal("ambiguous recovery accepted", ok, err)
	}
	stages.provisioning = []*wiredProvisionExecutor{&other, executor}
	prerequisite.write = &configurationWrite{resource: "assignments/changed"}
	if ok, err := stages.verify(ctx, verifying); ok || !errors.Is(err, errConfigurationBusy) {
		t.Fatal("unsettled prerequisite ignored", ok, err)
	}
	prerequisite.write = nil
	if ok, err := stages.verify(ctx, verifying); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if checks < 5 || submits != 0 {
		t.Fatal("sequencing bypassed validation", checks, submits)
	}
}
