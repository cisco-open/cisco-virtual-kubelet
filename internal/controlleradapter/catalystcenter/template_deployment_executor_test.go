// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
	"time"
)

func testRegularTemplateExecutor(t *testing.T, c *client, write *configurationWrite, target regularTemplateTarget, now time.Time, submitted metav1.Time) {
	t.Helper()
	for _, mode := range []string{"prepared", "prepared-source", "prepared-resource", "owner", "accepted", "verifying", "unknown"} {
		t.Run("executor-"+mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			a.client = c
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"edge","site":"Global/Lab/Building","state":"PROVISION","dayn_templates":{"regular":[{"name":"Lab#Baseline","variables":[{"name":"value","value":"secret-original"}]}]}}]}}}`)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			_, err := a.prepareConfiguration(ctx, config, nil, "inventory", write.resource, write.action, write.identity)
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			op := nc.Status.Configuration.Operation
			switch mode {
			case "prepared-source":
				op.SourceVersion = "other"
			case "prepared-resource":
				op.Resource = "template-deploy:base/other/3/" + op.Resource[len(op.Resource)-64:]
			case "owner":
				nc.Status.Configuration.Owners = nil
			case "accepted":
				op.Phase = "Accepted"
			case "verifying":
				op.Phase = "Verifying"
			case "unknown":
				op.Phase = "Unknown"
			}
			if mode == "accepted" || mode == "verifying" {
				op.TaskID = deploymentFixtureID
				op.SubmittedAt = &submitted
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			executor := &regularTemplateExecutor{adapter: a, config: config, device: inventoryDeviceIntent{reference: configurationDeviceReference{Name: "edge"}, site: "Global/Lab/Building"}, target: target}
			prior := &wirelessStageFixture{err: errConfigurationBusy}
			stage := &templateInventoryStages{adapter: a, prerequisites: configurationApplyPlan{section: "inventory", executor: prior}, templates: []templateStageBinding{{executor: executor, deviceID: op.TargetID, serial: op.TargetSerial, baseID: "base"}}, validate: func(context.Context) error { return nil }}
			planned, err := stage.next(ctx)
			if prior.calls != 0 {
				t.Fatal("retained deployment routed to prerequisite planning")
			}
			if mode == "prepared" {
				if err != nil || planned == nil || planned.resource != op.Resource {
					t.Fatal("prepared plan not resumed", err)
				}
			} else if err == nil || planned != nil {
				t.Fatal("nonmatching or submitted operation replanned")
			}
			if mode == "accepted" {
				done, err := stage.observeCompletion(ctx, op, now)
				if err != nil || !done {
					t.Fatal("accepted receipt not observed", err)
				}
				if done, err := stage.verify(ctx, op); done || err == nil {
					t.Fatal("accepted operation verified too early")
				}
			}
			if mode == "verifying" {
				done, err := stage.verify(ctx, op)
				if err != nil || !done {
					t.Fatal("deployment not verified", err)
				}
				if done, err := stage.observeCompletion(ctx, op, now); done || err == nil {
					t.Fatal("wrong completion phase accepted")
				}
				stage.templates = append(stage.templates, stage.templates[0])
				if done, err := stage.verify(ctx, op); done || err == nil {
					t.Fatal("ambiguous template route accepted")
				}
			}
		})
	}
}
