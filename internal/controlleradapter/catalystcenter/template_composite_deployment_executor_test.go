// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
	"time"
)

func testCompositeExecutor(t *testing.T, native *client, device inventoryDeviceIntent, target compositeTemplateTarget, write *configurationWrite, submitted time.Time) {
	t.Helper()
	for _, mode := range []string{"prepared", "graph", "owner", "source", "receipt", "accepted", "verifying", "unknown"} {
		t.Run("executor-"+mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			a.client = native
			config.Spec.ManagedSections = []string{"inventory"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			if _, err := a.prepareConfiguration(ctx, config, nil, "inventory", write.resource, write.action, write.identity); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			op := nc.Status.Configuration.Operation
			switch mode {
			case "graph":
				op.DependencyVersions.Entries[1].Version++
			case "owner":
				nc.Status.Configuration.Owners = nil
			case "source":
				op.SourceVersion = "changed"
			case "receipt":
				op.TaskID = deploymentFixtureID
			case "accepted":
				op.Phase = "Accepted"
			case "verifying":
				op.Phase = "Verifying"
			case "unknown":
				op.Phase = "Unknown"
			}
			if mode == "accepted" || mode == "verifying" {
				op.TaskID = deploymentFixtureID
				op.SubmittedAt = &metav1.Time{Time: submitted}
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			op = journalState(t, a).Operation.DeepCopy()
			e := &compositeTemplateExecutor{adapter: a, config: config, device: device, target: target}
			prior := &wirelessStageFixture{err: errConfigurationBusy}
			stage := &templateInventoryStages{adapter: a, prerequisites: configurationApplyPlan{section: "inventory", executor: prior}, templates: []templateStageBinding{{executor: e, deviceID: op.TargetID, serial: op.TargetSerial, baseID: "composite"}}, validate: func(context.Context) error { return nil }}
			planned, err := stage.next(ctx)
			if prior.calls != 0 {
				t.Fatal("retained composite routed to prerequisites")
			}
			if mode == "prepared" {
				if err != nil || planned == nil || planned.resource != op.Resource {
					t.Fatal("exact prepared resume failed", err)
				}
			} else if err == nil || planned != nil {
				t.Fatal("invalid or submitted operation returned write")
			}
			if mode == "accepted" {
				if done, err := stage.observeCompletion(ctx, op, time.Now()); err != nil || !done {
					t.Fatal("accepted observation failed", err)
				}
				if done, err := stage.verify(ctx, op); err == nil || done {
					t.Fatal("accepted operation verified without phase transition")
				}
			}
			if mode == "verifying" {
				if done, err := stage.verify(ctx, op); err != nil || !done {
					t.Fatal("verification failed", err)
				}
				if done, err := stage.observeCompletion(ctx, op, time.Now()); err == nil || done {
					t.Fatal("wrong observation phase accepted")
				}
			}
			if e.count() != 1 {
				t.Fatal("wrong executor count")
			}
		})
	}
}
