// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

var wirelessTagRecreationExecutionModes = []string{"membership-prune-recreate", "membership-prune-recreate-stable-id", "membership-prune-recreate-repeat-fresh", "membership-prune-recreate-repeat-stable", "membership-prune-recreate-capacity", "membership-prune-recreate-reappear", "membership-prune-recreate-key", "membership-prune-recreate-preflight", "membership-prune-recreate-lost-ack", "membership-prune-recreate-child-failed", "membership-prune-recreate-tree-missing", "membership-prune-recreate-partial"}

func (q *wirelessTagReceiptQualification) repeatingRecreation() bool {
	return q.mode == "membership-prune-recreate-repeat-fresh" || q.mode == "membership-prune-recreate-repeat-stable"
}

func (q *wirelessTagReceiptQualification) stableRecreationIDs() bool {
	return q.mode == "membership-prune-recreate-stable-id" || q.mode == "membership-prune-recreate-repeat-stable"
}

func (q *wirelessTagReceiptQualification) qualifyRecreation(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, tick func(), recovery *wirelessProfileRecoveryQualification, tags map[string][]map[string]any, model func(string) []byte, cohort bool) bool {
	t.Helper()
	old, err := wirelessTagRecords(journalState(t, a))
	if err != nil {
		t.Fatal(err)
	}
	oldLatest, _ := wirelessTagLatest(old)
	q.stage = 3
	q.recreateEpoch++
	beforeWrites := recovery.writes
	config.Spec.Source.Inline.Raw = model("WLAN2")
	switch q.mode {
	case "membership-prune-recreate-capacity":
		q.fill(t, a, 256)
	case "membership-prune-recreate-reappear":
		old := q.pruned[0]
		tags[old.kind] = append(tags[old.kind], old.row)
	case "membership-prune-recreate-key":
		if err := os.WriteFile(q.keyPath, []byte(strings.Repeat("e", 64)), 0600); err != nil {
			t.Fatal(err)
		}
	case "membership-prune-recreate-partial":
		var root map[string]any
		if json.Unmarshal(config.Spec.Source.Inline.Raw, &root) != nil {
			t.Fatal("bad partial-cohort source")
		}
		profile := root["catalyst_center"].(map[string]any)["network_profiles"].(map[string]any)["wireless"].([]any)[0].(map[string]any)
		profile["policy_tags"] = profile["policy_tags"].([]any)[:1]
		config.Spec.Source.Inline.Raw, _ = json.Marshal(root)
	}
	for _, mode := range []string{"lost-ack", "child-failed", "tree-missing"} {
		if q.mode == "membership-prune-recreate-"+mode {
			recovery.mode, recovery.target = mode, "site-tag"
		}
	}
	config.Generation++
	if err := a.statusWriter.Update(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	beforeLedger := journalState(t, a).DeepCopy().ActionApplications
	settled := false
	successful := q.mode == "membership-prune-recreate" || q.stableRecreationIDs() || q.repeatingRecreation()
	limit := 60
	if !successful {
		limit = 12
	}
	for range limit {
		tick()
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && journalState(t, a).Operation == nil {
			settled = true
			break
		}
	}
	if !successful {
		state := journalState(t, a)
		if !reflect.DeepEqual(beforeLedger, state.ActionApplications) || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("failed recreation published partial archive/new ownership or InSync")
		}
		expected := beforeWrites
		if recovery.interrupted != nil {
			expected++
			recovery.assertRetained(t, a, config, recovery.writes)
		}
		if recovery.writes != expected {
			t.Fatal("failed recreation replayed or dispatched outside its selected operation")
		}
		if q.mode == "membership-prune-recreate-preflight" && !q.changed {
			t.Fatal("late native resurrection fault was not exercised")
		}
		if q.mode == "membership-prune-recreate-lost-ack" || q.mode == "membership-prune-recreate-child-failed" || q.mode == "membership-prune-recreate-tree-missing" {
			if recovery.interrupted == nil {
				t.Fatal("uncertain acknowledged/reply-lost recreation was not exercised")
			}
		}
		return true
	}
	if !settled {
		t.Fatal("manifest-driven recreation did not converge", config.Status.Conditions, journalState(t, a).Operation)
	}
	state := journalState(t, a)
	rows, err := wirelessTagRecords(state)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := wirelessTagLatest(rows)
	archives, archiveErr := wirelessTagRetiredCohorts(state, rows)
	expected := 2
	if cohort {
		expected = 3
	}
	if err != nil || archiveErr != nil || len(archives) != 2 || len(rows) != 2 || len(latest) != expected || recovery.writes != beforeWrites+2 || q.tagDeletes != expected {
		t.Fatal("recreation lost complete producers, original tasks or exact write counts", err, archiveErr)
	}
	for key, proof := range latest {
		if !wirelessTagRecreateSelected(proof.row.Operation.Resource) || proof.mac == oldLatest[key].mac || proof.row.Operation.TaskID == "" || proof.row.Operation.TaskID == oldLatest[key].row.Operation.TaskID {
			t.Fatal("new incarnation borrowed prior private effect/task history")
		}
		if (proof.id == oldLatest[key].id) != q.stableRecreationIDs() {
			t.Fatal("native identifier stability scenario was not exercised")
		}
	}
	if len(tags["siteTags"])+len(tags["policyTags"]) != expected {
		t.Fatal("native recreation omitted producer members")
	}
	for range 3 {
		tick()
	}
	if recovery.writes != beforeWrites+2 || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || journalState(t, a).Operation != nil {
		t.Fatal("recreation no-op replayed or lost private custody")
	}
	t.Log("manifest reintroduction authenticated a new incarnation; complete predecessor archives and new creators published atomically; distinct acknowledged tasks recovered and final no-op verified")
	if q.repeatingRecreation() {
		q.qualifyRepeatedRecreation(t, a, config, tick, recovery, latest, expected)
	}
	return true
}

func TestWirelessTagRecreationExecution(t *testing.T) {
	for _, cohort := range []bool{false, true} {
		for _, mode := range wirelessTagRecreationExecutionModes {
			t.Run(fmt.Sprintf("cohort-%t/%s", cohort, mode), func(t *testing.T) {
				if !cohort && mode == "membership-prune-recreate-partial" {
					t.Skip("partial bulk producer requires a cohort")
				}
				exerciseWirelessTagReceipts(t, cohort, mode, nil)
			})
		}
	}
}
