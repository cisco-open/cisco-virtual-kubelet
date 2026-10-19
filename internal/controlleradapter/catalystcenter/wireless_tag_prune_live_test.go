//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func (h *liveNetworkProfileRecovery) privatePrunePlan(t *testing.T, ctx context.Context, c *client) configurationApplyPlan {
	t.Helper()
	nc, err := h.a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, h.a.statusReader, h.config)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := h.a.compileWirelessTagComparison(nc, h.config, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.a.bindProfileSiteApplications(nc, h.config, source, &plan); err != nil {
		t.Fatal(err)
	}
	if err := h.a.bindWirelessTagReceipts(nc, h.config, source, &plan); err != nil {
		t.Fatal(err)
	}
	h.a.bindNetworkProfileTaskRecovery(h.config, source, &plan)
	if err := h.a.bindProfileSitePrune(nc, h.config, source, &plan); err != nil {
		t.Fatal(err)
	}
	return plan
}
func (h *liveNetworkProfileRecovery) pruneTagsAndAssociations(t *testing.T, ctx context.Context, c *client, tags, assignments int) {
	t.Helper()
	h.preparePrune(t, ctx)
	before, err := wirelessTagRecords(journalState(t, h.a))
	if err != nil {
		t.Fatal(err)
	}
	expectedRows := len(before)
	prior, err := wirelessTagLatest(before)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range prior {
		if member.row.Operation.Action == "Create" {
			expectedRows++ // first application slot of a fresh incarnation
		}
	}
	for step := 0; step <= tags+assignments; step++ {
		plan := h.privatePrunePlan(t, ctx, c)
		write, err := plan.executor.next(ctx)
		if err != nil {
			t.Fatal("native full-tag prune planning refused; retain fixtures", err)
		}
		if step == tags+assignments {
			if write != nil {
				t.Fatal("extra deletion outside owned fixture scope")
			}
			break
		}
		if write == nil || write.action != "Delete" {
			t.Fatal("expected private tag or strict association DELETE")
		}
		if step < tags {
			if !wirelessTagPruneSelected(write.resource) {
				t.Fatal("association planned before complete actual tag absence")
			}
		} else if !profileSiteRemovalSelected(&cvk.NetworkControllerConfigurationOperation{Resource: write.resource}) {
			t.Fatal("tag prune did not precede unassignment")
		}
		h.run(t, ctx, c, h.config.Spec.Source.Inline.Raw, write)
	}
	after, err := wirelessTagRecords(journalState(t, h.a))
	if err != nil || len(after) != expectedRows {
		t.Fatal("tag deletion failed bounded application-slot accounting", err)
	}
	latest, err := wirelessTagLatest(after)
	if err != nil || len(latest) != tags {
		t.Fatal("tag tombstone member count mismatch", err)
	}
	for _, row := range latest {
		if row.row.Operation.Action != "Delete" {
			t.Fatal("tag missing its original-task tombstone")
		}
	}
	pairs, err := profileSiteApplicationRecords(journalState(t, h.a))
	if err != nil || len(pairs) != len(h.associationTasks) {
		t.Fatal("association tombstone count mismatch", err)
	}
	for _, row := range pairs {
		if row.Operation.Action != "Delete" {
			t.Fatal("association not removed")
		}
	}
	h.finish(t, ctx, c, len(h.recovered))
	t.Logf("single source completed %d manifest-driven whole-tag DELETEs before %d strict association DELETEs; original tasks, atomic tombstones and final no-op verified", tags, assignments)
}
