// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// Only generated unassociated definitions are edited. This probe uses private
// prepared writes outside the config journal, then restores both definitions
// before the ordinary public manifest/recovery sequence proceeds.
func qualifyWirelessFeatureCompanionFence(t *testing.T, ctx context.Context, c *client, source []byte, caTrace *cleanAirManifestTrace, fraTrace *fraManifestTrace, ca wirelessCleanAir, fra wirelessFRA, save func(string, any)) {
	t.Helper()
	plan := func(selected string) *configurationWrite {
		t.Helper()
		var root map[string]any
		if err := json.Unmarshal(source, &root); err != nil {
			t.Fatal(err)
		}
		wireless := root["catalyst_center"].(map[string]any)["feature_templates"].(map[string]any)["wireless"].(map[string]any)
		if selected == "fra" {
			wireless["rrm_fra"].([]any)[0].(map[string]any)["fra_interval"] = 6
		} else {
			wireless["cleanair"].([]any)[0].(map[string]any)["device_reporting"] = !ca.Attributes.Reporting
		}
		raw, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		compiled, err := c.compileConfigurationApply(raw, []string{"feature_templates"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		write, err := compiled.executor.next(ctx)
		target, prefix := fra.ID, "rrm_fra/rrm-fra:"
		if selected == "ca" {
			target = ca.ID
			prefix = "cleanair/cleanair:"
		}
		if err != nil || write == nil || write.action != "Update" || write.identity.target != target || !strings.HasPrefix(write.resource, prefix) {
			t.Fatal("cohort probe plan", selected, err)
		}
		return write
	}
	run := func(write *configurationWrite, label string) {
		t.Helper()
		if write == nil || write.action != "Update" {
			t.Fatal("missing generated-only probe update", label)
		}
		started := time.Now()
		receipt, err := write.submit(ctx)
		if err != nil {
			t.Fatal("probe submission uncertain; retain joint fixture", label, err)
		}
		save(label+"-task.json", receipt)
		t.Log("generated-only cohort probe", label, write.identity.target, receipt.ID)
		if err = waitSiteCredentialTask(ctx, c, receipt, started); err != nil {
			t.Fatal("probe task incomplete; retain joint fixture", label, err)
		}
	}
	caWrite := func(reporting bool) *configurationWrite {
		t.Helper()
		want := ca
		want.ID = ""
		want.Attributes.Reporting = reporting
		write, err := (&wirelessCleanAirExecutor{client: c, desired: []wirelessCleanAir{want}}).next(ctx)
		if err != nil || write == nil || write.identity.target != ca.ID {
			t.Fatal("generated-only CA probe", err)
		}
		return write
	}
	fraWrite := func(freeze bool) *configurationWrite {
		t.Helper()
		want := fra
		want.ID = ""
		want.Attributes.Freeze = freeze
		write, err := (&wirelessFRAExecutor{client: c, desired: []wirelessFRA{want}}).next(ctx)
		if err != nil || write == nil || write.identity.target != fra.ID {
			t.Fatal("generated-only FRA probe", err)
		}
		return write
	}
	refuse := func(write *configurationWrite, label string) {
		t.Helper()
		attempts := caTrace.attempts + fraTrace.attempts
		if _, err := write.submit(ctx); !errors.Is(err, errConfigurationStale) {
			t.Fatal("changed companion did not refuse cohort write", label, err)
		}
		if _, err := write.submit(ctx); err != errConfigurationTransition {
			t.Fatal("stale cohort permit reusable", label, err)
		}
		if caTrace.attempts+fraTrace.attempts != attempts {
			t.Fatal("stale cohort reached mutation transport", label)
		}
		t.Log("cohort companion change refused without mutation attempt", label)
	}
	caStart, fraStart := caTrace.puts, fraTrace.puts
	old := plan("fra")
	caTrace.maxPuts = caStart + 1
	run(caWrite(!ca.Attributes.Reporting), "cohort-ca-edit")
	refuse(old, "planned-FRA/changed-CA")
	caTrace.maxPuts = caStart + 2
	run(caWrite(ca.Attributes.Reporting), "cohort-ca-restore")
	old = plan("ca")
	fraTrace.maxPuts = fraStart + 1
	run(fraWrite(!fra.Attributes.Freeze), "cohort-fra-edit")
	refuse(old, "planned-CA/changed-FRA")
	fraTrace.maxPuts = fraStart + 2
	run(fraWrite(fra.Attributes.Freeze), "cohort-fra-restore")
	// Verify complete serialized typed readback after restoring each probe field.
	for _, kind := range []string{wirelessFRAKind, wirelessCleanAirKind} {
		instances, err := c.wirelessFeatureInstances(ctx, kind)
		if err != nil {
			t.Fatal(err)
		}
		var current, baseline any
		if kind == wirelessFRAKind {
			current, err = c.wirelessFRARead(ctx, instances[fra.Name])
			baseline = fra
		} else {
			current, err = c.wirelessCleanAirRead(ctx, instances[ca.Name])
			baseline = ca
		}
		if err != nil {
			t.Fatal(err)
		}
		actualJSON, err := json.Marshal(current)
		if err != nil {
			t.Fatal(err)
		}
		beforeJSON, err := json.Marshal(baseline)
		if err != nil || string(actualJSON) != string(beforeJSON) {
			t.Fatal("cohort probe baseline not restored", kind, err)
		}
	}
	if caTrace.puts != caStart+2 || fraTrace.puts != fraStart+2 {
		t.Fatal("unexpected probe write count")
	}
	save("cohort-probes-complete.json", map[string]any{"restored": true, "refusedWithoutMutation": 2, "caProbePuts": 2, "fraProbePuts": 2})
}
