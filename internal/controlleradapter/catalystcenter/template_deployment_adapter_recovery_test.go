// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Called from the real native deployment/status fixture. Fake Kubernetes
// exercises source/owner/journal races, not actor or reservation qualification.
func testOwnedTemplateRecovery(t *testing.T, c *client, write *configurationWrite, target regularTemplateTarget, now time.Time, submitted metav1.Time, duringRead func(func())) {
	t.Helper()
	for _, mode := range []string{"same", "record", "record-owner", "record-late-source", "record-late-journal", "owner", "no-owner", "duplicate-owner", "source", "config-generation", "controller-generation", "wrong-operation", "late-source", "late-journal"} {
		t.Run("owned-"+mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			a.client = c
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"edge","site":"Global/Lab/Building","state":"PROVISION","dayn_templates":{"regular":[{"name":"Lab#Baseline","variables":[{"name":"value","value":"secret-original"}]}]}}]}}}`)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			op, err := a.prepareConfiguration(ctx, config, nil, "inventory", write.resource, write.action, write.identity)
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration.Operation.Phase = "Accepted"
			if strings.HasPrefix(mode, "record") {
				nc.Status.Configuration.Operation.Phase = "Verifying"
			}
			nc.Status.Configuration.Operation.TaskID = deploymentFixtureID
			nc.Status.Configuration.Operation.SubmittedAt = &submitted
			switch strings.TrimPrefix(mode, "record-") {
			case "owner":
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
			case "no-owner":
				nc.Status.Configuration.Owners = nil
			case "duplicate-owner":
				nc.Status.Configuration.Owners = append(nc.Status.Configuration.Owners, nc.Status.Configuration.Owners[0])
			case "source":
				nc.Status.Configuration.Operation.SourceVersion = "other"
			case "config-generation":
				nc.Status.Configuration.Operation.ConfigGeneration++
			case "controller-generation":
				nc.Status.Configuration.Operation.ControllerGeneration++
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			op = nc.Status.Configuration.Operation.DeepCopy()
			if mode == "wrong-operation" {
				op.ID = "other"
			}
			if strings.TrimPrefix(mode, "record-") == "late-source" {
				duringRead(func() {
					changed := config.DeepCopy()
					changed.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"devices":[]}}}`)
					if err := a.statusWriter.Update(ctx, changed); err != nil {
						t.Error(err)
					}
				})
			}
			if strings.TrimPrefix(mode, "record-") == "late-journal" {
				duringRead(func() {
					changed, err := a.currentController(ctx)
					if err != nil {
						t.Error(err)
						return
					}
					changed.Status.Configuration.Operation.Phase = "Unknown"
					if err := a.statusWriter.Status().Update(ctx, changed); err != nil {
						t.Error(err)
					}
				})
			}
			defer duringRead(nil)
			if strings.HasPrefix(mode, "record") {
				err := a.recordOwnedTemplateDeployment(ctx, config, nil, inventoryDeviceIntent{reference: configurationDeviceReference{Name: "edge"}, site: "Global/Lab/Building"}, target, op.ID, now)
				if mode != "record" {
					if err == nil {
						t.Fatal("invalid verification published")
					}
					fresh, readErr := a.currentController(ctx)
					if readErr != nil {
						t.Fatal(readErr)
					}
					if fresh.Status.Configuration.Operation.Phase == "Verified" || len(fresh.Status.Configuration.ActionApplications) != 0 {
						t.Fatal("failed validation retained completion")
					}
					return
				}
				if err != nil {
					t.Fatal("receipt write failed", err)
				}
				fresh, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				state := fresh.Status.Configuration
				if state.Operation.Phase != "Verified" || len(state.ActionApplications) != 1 || state.ActionApplications[0].Operation.ID != op.ID || state.ActionApplications[0].Operation.TaskID != op.TaskID || state.ActionApplications[0].VerifiedAt == nil {
					t.Fatal("verification and receipt not retained together")
				}
				if err := a.recordOwnedTemplateDeployment(ctx, config, nil, inventoryDeviceIntent{}, target, op.ID, now); err == nil {
					t.Fatal("repeated completion transition accepted")
				}
				// Model a journal already cleared by settlement; this fake
				// fixture does not exercise physical reservation settlement.
				state.Operation = nil
				if err := a.statusWriter.Status().Update(ctx, fresh); err != nil {
					t.Fatal(err)
				}
				planned, err := a.planOwnedTemplateDeployment(ctx, config, nil, inventoryDeviceIntent{reference: configurationDeviceReference{Name: "edge"}, site: "Global/Lab/Building"}, target)
				if err != nil || planned != nil {
					t.Fatal("retained completion did not suppress repeat", err)
				}
				return
			}
			done, err := a.observeOwnedTemplateDeployment(ctx, config, nil, inventoryDeviceIntent{reference: configurationDeviceReference{Name: "edge"}, site: "Global/Lab/Building"}, target, op, now)
			if done != (mode == "same") || (err == nil) != (mode == "same") {
				t.Fatal(mode, "unexpected owned recovery", err)
			}
			fresh, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			expected := "Accepted"
			if strings.TrimPrefix(mode, "record-") == "late-journal" {
				expected = "Unknown"
			}
			if fresh.Status.Configuration.Operation.Phase != expected || len(fresh.Status.Configuration.ActionApplications) != 0 {
				t.Fatal("observer changed journal or published completion")
			}
		})
	}
}
