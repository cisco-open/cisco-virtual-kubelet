// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

func (q *wirelessTagReceiptQualification) qualifyRepeatedRecreation(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, tick func(), recovery *wirelessProfileRecoveryQualification, first map[string]wirelessTagMemberProof, members int) {
	t.Helper()
	ctx := context.Background()
	var model map[string]any
	if json.Unmarshal(config.Spec.Source.Inline.Raw, &model) != nil {
		t.Fatal("invalid repeat-cycle source")
	}
	profile := model["catalyst_center"].(map[string]any)["network_profiles"].(map[string]any)["wireless"].([]any)[0].(map[string]any)
	apply := func() {
		t.Helper()
		config.Spec.Source.Inline.Raw, _ = json.Marshal(model)
		config.Generation++
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		for range 90 {
			tick() // each tick constructs a fresh reconciler using retained storage
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && journalState(t, a).Operation == nil {
				return
			}
		}
		t.Fatal("repeated incarnation transition did not settle", config.Status.Conditions, journalState(t, a).Operation)
	}
	observe := func(archives int, mode, action string) map[string]wirelessTagMemberProof {
		t.Helper()
		rows, err := wirelessTagRecords(journalState(t, a))
		if err != nil {
			t.Fatal(err)
		}
		latest, err := wirelessTagLatest(rows)
		retired, archiveErr := wirelessTagRetiredCohorts(journalState(t, a), rows)
		if err != nil || archiveErr != nil || len(latest) != members || len(retired) != archives {
			t.Fatal("repeated transition lost complete member/history coverage", err, archiveErr)
		}
		for key, proof := range latest {
			if proof.id != first[key].id {
				t.Fatal("same-incarnation update/delete replaced its native key")
			}
			epoch, _ := wirelessTagLineageDigest(proof.row.Operation.Resource)
			original, _ := wirelessTagLineageDigest(first[key].row.Operation.Resource)
			wantMode, wantAction := mode, action
			if mode == "ordinary" {
				wantMode, wantAction = "recreate", "Create"
				if strings.HasPrefix(key, "wireless-site-tags/") {
					wantMode, wantAction = "write", "Update"
				}
			}
			if epoch != original || proof.row.Operation.Action != wantAction {
				t.Fatal("update/delete lost the existing incarnation")
			}
			if wantMode != "" && wirelessTagLineageMode(proof.row.Operation.Resource) != wantMode {
				t.Fatal("wrong repeated transition class")
			}
		}
		return latest
	}

	before := recovery.writes
	profile["site_tags"].([]any)[0].(map[string]any)["ap_profile_name"] = "AP-OTHER"
	apply()
	updated := observe(2, "ordinary", "")
	for key, proof := range updated {
		if (proof.mac != first[key].mac) != strings.HasPrefix(key, "wireless-site-tags/") {
			t.Fatal("ordinary site-tag update changed a sibling proof or retained its stale baseline")
		}
	}
	if recovery.writes != before+1 {
		t.Fatal("ordinary v5 site-tag update did not issue exactly one mutation")
	}

	q.stage = 4
	for _, kind := range []string{"site_tags", "policy_tags"} {
		for _, value := range profile[kind].([]any) {
			row := value.(map[string]any)
			row["sites"] = row["sites"].([]any)[:1]
		}
	}
	before = recovery.writes
	apply()
	observe(2, "members", "Update")
	if recovery.writes != before+members {
		t.Fatal("v5 membership reduction lost per-member exact mutation counts")
	}
	reduced, _ := json.Marshal(model)

	q.stage = 2
	profile["site_tags"], profile["policy_tags"] = []any{}, []any{}
	before = recovery.writes
	apply()
	deleted := observe(2, "delete", "Delete")
	if recovery.writes != before+members || q.tagDeletes != 2*members {
		t.Fatal("new-incarnation pruning replayed or omitted a member")
	}

	if json.Unmarshal(reduced, &model) != nil {
		t.Fatal("invalid reduced reintroduction source")
	}
	q.stage = 3
	q.recreateEpoch++
	before = recovery.writes
	apply()
	rows, err := wirelessTagRecords(journalState(t, a))
	latest, latestErr := wirelessTagLatest(rows)
	retired, archiveErr := wirelessTagRetiredCohorts(journalState(t, a), rows)
	if err != nil || latestErr != nil || archiveErr != nil || len(retired) != 4 || len(rows) != 2 || len(latest) != members || recovery.writes != before+2 {
		t.Fatal("second recreated producer did not retain both complete predecessor generations", err, latestErr, archiveErr)
	}
	for key, proof := range latest {
		epoch, ok := wirelessTagRecreateParent(proof.row.Operation.Resource)
		old, _ := wirelessTagLineageDigest(first[key].row.Operation.Resource)
		if !ok || epoch == old || proof.mac == deleted[key].mac || proof.row.Operation.TaskID == deleted[key].row.Operation.TaskID || (proof.id == first[key].id) != q.stableRecreationIDs() {
			t.Fatal("second recreation reused old incarnation/task proof or wrong identity scenario")
		}
	}
	for range 3 {
		tick()
	}
	if recovery.writes != before+2 || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || journalState(t, a).Operation != nil {
		t.Fatal("second recreation no-op replayed or lost private custody")
	}
	t.Log("two manifest recreation cycles retained four complete archive cohorts; ordinary v5 update, membership reduction and deletion preserved incarnation identity; final no-op verified")
}
