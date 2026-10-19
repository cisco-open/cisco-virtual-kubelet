// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testOwnedCompositeRecovery(t *testing.T, native *client, device inventoryDeviceIntent, target compositeTemplateTarget, write *configurationWrite, submitted time.Time, setReadHook func(func())) {
	t.Helper()
	for _, mode := range []string{"stable", "foreign-owner", "missing-owner", "wrong-graph", "late-source", "late-journal", "record", "record-owner", "record-late-source", "record-late-journal"} {
		t.Run("owned-"+mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			a.client = native
			config.Spec.ManagedSections = []string{"inventory"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			op, err := a.prepareConfiguration(ctx, config, nil, "inventory", write.resource, "Update", write.identity)
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			op.Phase = "Accepted"
			if strings.HasPrefix(mode, "record") {
				op.Phase = "Verifying"
			}
			op.TaskID = deploymentFixtureID
			op.SubmittedAt = &metav1.Time{Time: submitted}
			nc.Status.Configuration.Operation = op.DeepCopy()
			if mode == "foreign-owner" || mode == "record-owner" {
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
			}
			if mode == "missing-owner" {
				nc.Status.Configuration.Owners = nil
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			op = journalState(t, a).Operation.DeepCopy()
			if mode == "wrong-graph" {
				op.DependencyVersions.Entries[1].Version++
			}
			if mode == "late-source" || mode == "late-journal" || mode == "record-late-source" || mode == "record-late-journal" {
				setReadHook(func() {
					if mode == "late-source" || mode == "record-late-source" {
						changed := config.DeepCopy()
						changed.Generation++
						if err := a.statusWriter.Update(ctx, changed); err != nil {
							t.Error(err)
						}
					} else {
						changed, err := a.currentController(ctx)
						if err != nil {
							t.Error(err)
							return
						}
						changed.Status.Configuration.Operation.DependencyVersions.Entries[1].Version++
						if err := a.statusWriter.Status().Update(ctx, changed); err != nil {
							t.Error(err)
						}
					}
				})
			}
			if strings.HasPrefix(mode, "record") {
				err := a.recordOwnedCompositeTemplateDeployment(ctx, config, nil, device, target, op.ID, time.Now())
				setReadHook(nil)
				state := journalState(t, a)
				if mode == "record" {
					if err != nil || state.Operation.Phase != "Verified" || len(state.ActionApplications) != 1 || !reflect.DeepEqual(state.ActionApplications[0].Operation.DependencyVersions, op.DependencyVersions) {
						t.Fatal("atomic composite receipt missing", err)
					}
					if err := a.recordOwnedCompositeTemplateDeployment(ctx, config, nil, device, target, op.ID, time.Now()); err == nil {
						t.Fatal("repeated verification accepted")
					}
					// Model settlement for planner qualification only; no actor/Lease
					// release is performed or inferred by this fixture.
					settled, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					settled.Status.Configuration.Operation = nil
					if err := a.statusWriter.Status().Update(ctx, settled); err != nil {
						t.Fatal(err)
					}
					planned, err := a.planOwnedCompositeTemplateDeployment(ctx, config, nil, device, target)
					if err != nil || planned != nil {
						t.Fatal("verified composite planned again", err)
					}
				} else if err == nil || state.Operation.Phase != "Verifying" || len(state.ActionApplications) != 0 {
					t.Fatal("stale completion published", err)
				}
				return
			}
			done, err := a.observeOwnedCompositeTemplateDeployment(ctx, config, nil, device, target, op, time.Now())
			setReadHook(nil)
			if mode == "stable" {
				if err != nil || !done {
					t.Fatal("owned recovery rejected", err)
				}
			} else if err == nil || done {
				t.Fatal("stale or foreign recovery accepted")
			}
			if journalState(t, a).Operation.Phase != "Accepted" || len(journalState(t, a).ActionApplications) != 0 {
				t.Fatal("observation advanced journal")
			}
		})
	}
}
