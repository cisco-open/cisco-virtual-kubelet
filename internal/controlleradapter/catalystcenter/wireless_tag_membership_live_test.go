//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"os"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

func (h *liveNetworkProfileRecovery) removeTagMembership(t *testing.T, ctx context.Context, c *client, raw []byte, expected int) string {
	t.Helper()
	h.config.Spec.Source.Inline.Raw = append([]byte{}, raw...)
	h.config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	h.config.Generation++
	if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
		t.Fatal(err)
	}
	before, err := wirelessTagRecords(journalState(t, h.a))
	if err != nil {
		t.Fatal(err)
	}
	combined := os.Getenv("CATC_PROFILE_TAG_DEPENDENCIES") == "isolated-private"
	total := expected
	if combined {
		total++
	}
	removed := ""
	for step := 0; step <= total; step++ {
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
		if combined {
			h.a.bindNetworkProfileTaskRecovery(h.config, source, &plan)
			if err := h.a.bindProfileSitePrune(nc, h.config, source, &plan); err != nil {
				t.Fatal(err)
			}
		}
		write, err := plan.executor.next(ctx)
		if err != nil {
			t.Fatal("private membership plan refused; retain fixture", err)
		}
		if step == total {
			if write != nil {
				t.Fatal("unexpected extra tag mutation")
			}
			break
		}
		if step < expected && (write == nil || write.action != "Update" || !wirelessTagPrivateSelected(write.resource)) {
			t.Fatal("expected private same-ID tag Update")
		}
		if combined && step == expected {
			if write == nil || write.action != "Delete" || !profileSiteRemovalSelected(&cvk.NetworkControllerConfigurationOperation{Resource: write.resource}) {
				t.Fatal("expected actual-reference-free association removal after tag Updates")
			}
			removed = write.identity.target
		}
		// The live mutation observer uses the public selector to match the adapter's
		// private execution envelope. This does not dispatch the planned closure.
		write.resource = wirelessTagCanonical(write.resource)
		h.run(t, ctx, c, raw, write)
	}
	after, err := wirelessTagRecords(journalState(t, h.a))
	if err != nil || len(before) != len(after) {
		t.Fatal("membership update did not replace bounded latest slots", err)
	}
	t.Logf("%d private same-UUID tag membership removals recovered through original task trees; retained row count unchanged at %d", expected, len(after))
	if combined {
		if !validAPIID(removed) {
			t.Fatal("combined manifest did not unassign the removed site")
		}
		t.Log("single complete source removed owned tag references before actual UUID-bound association unassignment", removed)
	}
	return removed
}
