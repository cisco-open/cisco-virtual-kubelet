//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

func TestLiveWirelessTagRecreationPublicRecovery(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_TAG_RECREATE") != "isolated-private" {
		t.Skip("requires explicit isolated tag recreation qualification")
	}
	qualifyLiveWirelessProfileExecution(t, true, false, "")
}

func TestLiveWirelessTagCohortRecreationPublicRecovery(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_TAG_RECREATE") != "isolated-private" {
		t.Skip("requires explicit isolated tag recreation qualification")
	}
	qualifyLiveWirelessProfileExecution(t, true, true, "")
}

func (h *liveNetworkProfileRecovery) recreateTags(t *testing.T, ctx context.Context, c *client, count int) {
	t.Helper()
	original := append([]byte{}, h.config.Spec.Source.Inline.Raw...)
	var model map[string]any
	decoder := json.NewDecoder(bytes.NewReader(original))
	decoder.UseNumber()
	if decoder.Decode(&model) != nil {
		t.Fatal("invalid native recreation source")
	}
	profiles := model["catalyst_center"].(map[string]any)["network_profiles"].(map[string]any)["wireless"].([]any)
	for _, value := range profiles {
		profile := value.(map[string]any)
		profile["site_tags"], profile["policy_tags"] = []any{}, []any{}
	}
	removed, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	h.config.Spec.Source.Inline.Raw = removed
	h.config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	h.config.Generation++
	if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
		t.Fatal(err)
	}
	for range count {
		write, err := h.privatePrunePlan(t, ctx, c).executor.next(ctx)
		if err != nil || write == nil || write.action != "Delete" || !wirelessTagPruneSelected(write.resource) {
			t.Fatal("expected only owned tag deletion before recreation; retain fixture", err)
		}
		h.run(t, ctx, c, removed, write)
	}
	before, err := wirelessTagRecords(journalState(t, h.a))
	if err != nil {
		t.Fatal(err)
	}
	priorArchives, err := wirelessTagRetiredCohorts(journalState(t, h.a), before)
	if err != nil {
		t.Fatal(err)
	}
	old, err := wirelessTagLatest(before)
	if err != nil || len(old) != count {
		t.Fatal("missing native predecessor tombstones", err)
	}
	for _, row := range old {
		if row.row.Operation.Action != "Delete" {
			t.Fatal("recreation preceded complete tag deletion")
		}
	}
	h.config.Spec.Source.Inline.Raw = original
	h.config.Generation++
	if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
		t.Fatal(err)
	}
	for range 2 { // one site producer and one singleton/bulk policy producer
		write, err := h.privatePrunePlan(t, ctx, c).executor.next(ctx)
		if err != nil || write == nil || write.action != "Create" || !wirelessTagRecreateSelected(write.resource) {
			t.Fatal("expected versioned complete-producer recreation; retain fixture", err)
		}
		h.run(t, ctx, c, original, write)
	}
	state := journalState(t, h.a)
	rows, err := wirelessTagRecords(state)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := wirelessTagLatest(rows)
	archives, archiveErr := wirelessTagRetiredCohorts(state, rows)
	if err != nil || archiveErr != nil || len(archives) != len(priorArchives)+2 || len(latest) != count {
		t.Fatal("native recreation omitted complete predecessor history", err, archiveErr)
	}
	stable := 0
	for key, row := range latest {
		if !wirelessTagRecreateSelected(row.row.Operation.Resource) || row.mac == old[key].mac || row.row.Operation.TaskID == old[key].row.Operation.TaskID {
			t.Fatal("native recreation reused private effects/tasks or lost lineage")
		}
		if row.id == old[key].id {
			stable++
		}
	}
	h.finish(t, ctx, c, len(h.recovered))
	t.Logf("%d native tag members recreated with authenticated new incarnations (stable native IDs: %d); %d complete predecessor cohorts archived atomically with original-task recovery and final InSync/no-op", count, stable, len(archives))
}
