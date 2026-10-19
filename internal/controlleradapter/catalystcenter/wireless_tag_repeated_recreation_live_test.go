//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestLiveWirelessTagRepeatedRecreationPublicRecovery(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_TAG_REPEAT") != "isolated-private" {
		t.Skip("requires explicit isolated repeated tag recreation qualification")
	}
	qualifyLiveWirelessProfileExecution(t, true, false, "")
}

func TestLiveWirelessTagCohortRepeatedRecreationPublicRecovery(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_TAG_REPEAT") != "isolated-private" {
		t.Skip("requires explicit isolated repeated tag recreation qualification")
	}
	qualifyLiveWirelessProfileExecution(t, true, true, "")
}

// Expand only into the second already-owned, still-associated empty area. This
// exercises an ordinary v5 write without introducing a new AP profile or any
// device assignment. Then remove that membership before another delete/create.
func (h *liveNetworkProfileRecovery) repeatTagRecreation(t *testing.T, ctx context.Context, c *client, paths []string, count int) {
	t.Helper()
	if len(paths) != 2 {
		t.Fatal("repeat qualification requires exactly two owned empty areas")
	}
	var model map[string]any
	if json.Unmarshal(h.config.Spec.Source.Inline.Raw, &model) != nil {
		t.Fatal("invalid native repeated recreation source")
	}
	profile := model["catalyst_center"].(map[string]any)["network_profiles"].(map[string]any)["wireless"].([]any)[0].(map[string]any)
	baseline := func() map[string]wirelessTagMemberProof {
		t.Helper()
		rows, err := wirelessTagRecords(journalState(t, h.a))
		latest, latestErr := wirelessTagLatest(rows)
		if err != nil || latestErr != nil || len(latest) != count {
			t.Fatal("incomplete repeated-cycle private baseline", err, latestErr)
		}
		return latest
	}
	first := baseline()
	for _, stage := range []struct {
		sites  []string
		mode   string
		writes int
	}{
		{paths, "write", count}, // existing tags use individual same-ID Updates
		{paths[:1], "members", count},
	} {
		for _, kind := range []string{"site_tags", "policy_tags"} {
			for _, value := range profile[kind].([]any) {
				value.(map[string]any)["sites"] = stage.sites
			}
		}
		raw, err := json.Marshal(model)
		if err != nil {
			t.Fatal(err)
		}
		h.config.Spec.Source.Inline.Raw = raw
		h.config.Generation++
		if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
			t.Fatal(err)
		}
		before := len(h.recovered)
		previous := baseline()
		for range stage.writes {
			write, err := h.privatePrunePlan(t, ctx, c).executor.next(ctx)
			if err != nil || write == nil || write.action != "Update" || wirelessTagLineageMode(write.resource) != stage.mode {
				t.Fatal("expected exact v5 repeated-cycle Update; retain fixture", stage.mode, err)
			}
			h.run(t, ctx, c, raw, write)
		}
		if len(h.recovered) != before+stage.writes {
			t.Fatal("repeated native transition omitted or replayed an original task")
		}
		for key, proof := range baseline() {
			epoch, ok := wirelessTagLineageDigest(proof.row.Operation.Resource)
			original, _ := wirelessTagLineageDigest(first[key].row.Operation.Resource)
			if !ok || epoch != original || proof.id != first[key].id || proof.mac == previous[key].mac || proof.row.Operation.TaskID == previous[key].row.Operation.TaskID || wirelessTagLineageMode(proof.row.Operation.Resource) != stage.mode {
				t.Fatal("native v5 update replaced identity/lineage or retained stale private evidence")
			}
		}
		h.finish(t, ctx, c, before+stage.writes)
		t.Logf("native v5 %s preserved %d incarnation identities with %d original tasks and final no-op", stage.mode, count, stage.writes)
	}

	before := len(h.recovered)
	h.recreateTags(t, ctx, c, count)
	if len(h.recovered) != before+count+2 {
		t.Fatal("second native recreation omitted or replayed a producer/member operation")
	}
	for key, proof := range baseline() {
		epoch, ok := wirelessTagLineageDigest(proof.row.Operation.Resource)
		original, _ := wirelessTagLineageDigest(first[key].row.Operation.Resource)
		if !ok || epoch == original || proof.mac == first[key].mac || proof.row.Operation.TaskID == first[key].row.Operation.TaskID {
			t.Fatal("second native recreation reused the first incarnation")
		}
	}
	t.Log("native repeated cycle preserved ordinary updates and membership reduction, then retained four complete predecessor cohorts; original-task recovery and final no-op verified")
}
