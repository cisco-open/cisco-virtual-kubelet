// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

var wirelessTagPruneModes = []string{"membership-prune-complete", "membership-prune-retain", "membership-prune-unowned", "membership-prune-native-edit", "membership-prune-uuid", "membership-prune-key", "membership-prune-absent-before", "membership-prune-device", "membership-prune-pnp", "membership-prune-read-error", "membership-prune-preflight-device", "membership-prune-preflight-peer", "membership-prune-post-task-device", "membership-prune-post-task-reappear", "membership-prune-settled-reappear", "membership-prune-recreate", "membership-prune-at-capacity", "membership-prune-created-only", "membership-prune-capacity", "membership-prune-lost-ack", "membership-prune-child-failed", "membership-prune-tree-missing", "membership-prune-source", "membership-prune-owner", "membership-prune-tombstone-tamper", "membership-prune-settled-device", "membership-prune-unknown-peer", "membership-prune-empty-members", "membership-prune-combined", "membership-prune-empty-kind"}

type wirelessTagPruneFixtureDeletion struct {
	kind string
	row  map[string]any
}

func (q *wirelessTagReceiptQualification) pruning() bool {
	return strings.HasPrefix(q.mode, "membership-prune-")
}
func (q *wirelessTagReceiptQualification) recreating() bool {
	return strings.HasPrefix(q.mode, "membership-prune-recreate")
}
func (q *wirelessTagReceiptQualification) startPruning(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, tags map[string][]map[string]any, tagWrites int) {
	t.Helper()
	q.stage = 2
	q.steps = 0
	q.pruneWrites = tagWrites
	var root map[string]any
	if json.Unmarshal(c.Spec.Source.Inline.Raw, &root) != nil {
		t.Fatal("bad fixture model")
	}
	p := root["catalyst_center"].(map[string]any)["network_profiles"].(map[string]any)["wireless"].([]any)[0].(map[string]any)
	if q.mode == "membership-prune-empty-members" {
		for _, kind := range []string{"site_tags", "policy_tags"} {
			for _, row := range p[kind].([]any) {
				row.(map[string]any)["sites"] = []any{}
			}
		}
	} else {
		p["site_tags"] = []any{}
		p["policy_tags"] = []any{}
	}
	if q.mode == "membership-prune-combined" {
		p["sites"] = []any{}
	}
	if q.mode == "membership-prune-empty-kind" {
		root["catalyst_center"].(map[string]any)["network_profiles"].(map[string]any)["wireless"] = []any{}
	}
	c.Spec.Source.Inline.Raw, _ = json.Marshal(root)
	c.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	if q.mode == "membership-prune-retain" {
		c.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
	}
	c.Generation++
	if err := a.statusWriter.Update(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	switch q.mode {
	case "membership-prune-unowned":
		nc, _ := a.currentController(context.Background())
		nc.Status.Configuration.ActionApplications = nil
		if err := a.statusWriter.Status().Update(context.Background(), nc); err != nil {
			t.Fatal(err)
		}
	case "membership-prune-native-edit":
		tags["siteTags"][0]["apProfileName"] = "AP-OTHER"
	case "membership-prune-uuid":
		tags["siteTags"][0]["siteTagId"] = "replacement"
	case "membership-prune-key":
		if err := os.WriteFile(q.keyPath, []byte(strings.Repeat("e", 64)), 0600); err != nil {
			t.Fatal(err)
		}
	case "membership-prune-absent-before":
		tags["policyTags"] = tags["policyTags"][1:]
	case "membership-prune-at-capacity":
		q.fill(t, a, 256)
	case "membership-prune-capacity":
		q.fill(t, a, 255)
	}
}
func (q *wirelessTagReceiptQualification) pruneRequest(t *testing.T, a *adapter, w http.ResponseWriter, r *http.Request, tags map[string][]map[string]any, assignments *[]map[string]string, recovery *wirelessProfileRecoveryQualification) bool {
	if q.recreating() && q.stage == 3 && q.mode == "membership-prune-recreate-preflight" && !q.changed {
		state := journalState(t, a)
		if state.Operation != nil && state.Operation.Phase == "Submitting" {
			kind := "siteTags"
			if strings.HasPrefix(state.Operation.Resource, "wireless-policy-tags/") {
				kind = "policyTags"
			}
			for _, original := range q.pruned {
				if original.kind == kind {
					tags[kind] = append(tags[kind], original.row)
					q.changed = true
					break
				}
			}
		}
	}
	if !q.pruning() || q.stage != 2 {
		return false
	}
	if q.mode == "membership-prune-unknown-peer" {
		found := false
		for _, row := range tags["policyTags"] {
			if row["policyTagId"] == "unknown-peer" {
				found = true
			}
		}
		if !found {
			tags["policyTags"] = append(tags["policyTags"], map[string]any{"policyTagId": "unknown-peer", "policyTagName": "UNKNOWN", "siteIds": []any{"lab-site"}, "apZones": []any{"default-zone"}, "futureReference": true})
		}
	}
	if q.mode == "membership-prune-preflight-peer" {
		state := journalState(t, a)
		if state.Operation != nil && state.Operation.Phase == "Submitting" {
			q.changed = true
			tags["siteTags"][0]["apProfileName"] = "AP-OTHER"
		}
	}
	if r.URL.Path == assignedDevicesPath {
		if q.mode == "membership-prune-read-error" {
			w.WriteHeader(503)
			return true
		}
		block := q.mode == "membership-prune-device" || q.mode == "membership-prune-settled-device" && q.changed
		if q.mode == "membership-prune-preflight-device" || q.mode == "membership-prune-post-task-device" {
			state := journalState(t, a)
			if state.Operation != nil && (q.mode == "membership-prune-preflight-device" && state.Operation.Phase == "Submitting" || q.mode == "membership-prune-post-task-device" && state.Operation.TaskID != "") {
				q.changed = true
			}
			block = q.changed
		}
		rows := []map[string]string{}
		if block && r.URL.Query().Get("siteId") == "lab-site" {
			rows = append(rows, map[string]string{"deviceId": "consumer", "siteId": "lab-site"})
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
		return true
	}
	if r.URL.Path == pnpDevicesPath {
		rows := []map[string]any{}
		if q.mode == "membership-prune-pnp" {
			rows = append(rows, map[string]any{"id": "pnp", "deviceInfo": map[string]any{"serialNumber": "FOC12345678", "pid": "C9300", "siteId": "lab-site", "state": "Claimed", "onbState": "Planned"}})
		}
		json.NewEncoder(w).Encode(rows)
		return true
	}
	if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, profileSiteAssignmentsPath("profile-id")+"/") {
		target := strings.TrimPrefix(r.URL.Path, profileSiteAssignmentsPath("profile-id")+"/")
		if (q.mode != "membership-prune-combined" && q.mode != "membership-prune-empty-kind") || (target != "lab-site" && target != "lab2-site") || len(tags["siteTags"])+len(tags["policyTags"]) != 0 {
			t.Error("association DELETE preceded actual whole-tag absence")
			w.WriteHeader(403)
			return true
		}
		kept := []map[string]string{}
		found := false
		for _, row := range *assignments {
			if row["id"] == target {
				found = true
			} else {
				kept = append(kept, row)
			}
		}
		if !found {
			t.Error("association DELETE replayed")
			w.WriteHeader(404)
			return true
		}
		*assignments = kept
		q.deletes++
		recovery.respondTask(t, a, w, time.Now().UnixMilli())
		return true
	}
	for _, policy := range []bool{false, true} {
		prefix := wirelessProfileTagPath("profile-id", policy) + "/"
		if r.Method != http.MethodDelete || !strings.HasPrefix(r.URL.Path, prefix) {
			continue
		}
		state := journalState(t, a)
		if state.Operation == nil || !wirelessTagPruneSelected(state.Operation.Resource) || state.Operation.TargetID != strings.TrimPrefix(r.URL.Path, prefix) {
			t.Error("unqualified tag DELETE")
			w.WriteHeader(403)
			return true
		}
		kind, idKey := "siteTags", "siteTagId"
		if policy {
			kind, idKey = "policyTags", "policyTagId"
		}
		kept := []map[string]any{}
		found := false
		for _, row := range tags[kind] {
			if row[idKey] == state.Operation.TargetID {
				q.pruned = append(q.pruned, wirelessTagPruneFixtureDeletion{kind: kind, row: row})
				found = true
			} else {
				kept = append(kept, row)
			}
		}
		if !found {
			t.Error("tag deletion replayed")
			w.WriteHeader(404)
			return true
		}
		tags[kind] = kept
		q.tagDeletes++
		recovery.respondTask(t, a, w, time.Now().UnixMilli())
		return true
	}
	return false
}
func (q *wirelessTagReceiptQualification) observePruning(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, tick func(), recovery *wirelessProfileRecoveryQualification, tags map[string][]map[string]any, tagWrites int, model func(string) []byte, cohort bool) bool {
	t.Helper()
	state := journalState(t, a)
	op := state.Operation
	expected := 2
	if cohort {
		expected = 3
	}
	if tagWrites != q.pruneWrites {
		t.Fatal("pruning replayed Create/Update")
	}
	if q.mode == "membership-prune-post-task-reappear" && op != nil && op.Phase == "Unknown" && op.TaskID != "" && !q.changed {
		q.changed = true
		restored := q.pruned[0]
		tags[restored.kind] = append(tags[restored.kind], restored.row)
	}
	blocked := !q.recreating() && q.mode != "membership-prune-complete" && q.mode != "membership-prune-created-only" && q.mode != "membership-prune-at-capacity" && q.mode != "membership-prune-retain" && q.mode != "membership-prune-unowned" && q.mode != "membership-prune-settled-reappear" && q.mode != "membership-prune-tombstone-tamper" && q.mode != "membership-prune-settled-device" && q.mode != "membership-prune-empty-members" && q.mode != "membership-prune-combined" && q.mode != "membership-prune-empty-kind"
	if blocked {
		if q.steps < 14 {
			return false
		}
		count := 0
		if q.mode == "membership-prune-post-task-device" || q.mode == "membership-prune-post-task-reappear" {
			count = 1
			if op == nil || op.TaskID == "" {
				t.Fatal("uncertain acknowledged deletion was released")
			}
			for _, row := range state.ActionApplications {
				if row.Operation.ID == op.ID {
					t.Fatal("unqualified tombstone published")
				}
			}
		}
		if q.tagDeletes != count || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("unsafe whole-tag pruning advanced", q.tagDeletes, c.Status.Conditions)
		}
		if strings.Contains(q.mode, "preflight-") && (!q.changed || op != nil) {
			t.Fatal("proven refusal was not reached/retired")
		}
		return true
	}
	if op != nil {
		if op.Phase == "Verified" && wirelessTagPruneSelected(op.Resource) && !wirelessTagReceiptPresent(state, op) {
			t.Fatal("Verified deletion lacks atomic tombstone")
		}
		return false
	}
	if c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
		return false
	}
	if q.mode == "membership-prune-retain" || q.mode == "membership-prune-unowned" {
		if q.tagDeletes != 0 || len(tags["siteTags"])+len(tags["policyTags"]) != expected {
			t.Fatal("retained/unowned tag was pruned")
		}
		return true
	}
	records, err := wirelessTagRecords(state)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := wirelessTagLatest(records)
	if err != nil || len(latest) != expected || q.tagDeletes != expected || len(tags["siteTags"])+len(tags["policyTags"]) != 0 {
		t.Fatal("whole-tag deletion did not converge", err, c.Status.Conditions)
	}
	for _, row := range latest {
		if row.row.Operation.Action != "Delete" {
			t.Fatal("deleted tag lacks latest tombstone")
		}
	}
	if q.mode == "membership-prune-combined" || q.mode == "membership-prune-empty-kind" {
		pairs, err := profileSiteApplicationRecords(state)
		if err != nil || len(pairs) != 2 || q.deletes != 2 {
			t.Fatal("combined tag/association cleanup incomplete", err)
		}
		for _, pair := range pairs {
			if pair.Operation.Action != "Delete" {
				t.Fatal("association missing tombstone")
			}
		}
	}
	if len(records) != expected+2 {
		t.Fatal("Create or sibling proofs lost")
	}
	if q.mode == "membership-prune-complete" {
		qualifyWirelessTagRecreationCohorts(t, state)
		qualifyWirelessTagRetiredChains(t, a, state)
	}
	if q.mode == "membership-prune-at-capacity" && len(state.ActionApplications) != 256 {
		t.Fatal("prune did not replace existing slots at capacity")
	}
	if q.mode == "membership-prune-tombstone-tamper" {
		nc, _ := a.currentController(context.Background())
		for i, row := range nc.Status.Configuration.ActionApplications {
			if row.Operation.Action == "Delete" && wirelessTagPruneSelected(row.Operation.Resource) {
				parts := strings.Split(row.EffectReceipts[0], ":")
				parts[2] = strings.Repeat("f", 64)
				row.EffectReceipts = []string{strings.Join(parts, ":")}
				suffix := row.Resource[strings.LastIndex(row.Resource, ":")+1:]
				row.Resource = wirelessTagReceiptBase(&row.Operation, row.EffectReceipts) + ":" + wirelessTagRowSeal(row) + ":" + suffix
				nc.Status.Configuration.ActionApplications[i] = row
				break
			}
		}
		if err := a.statusWriter.Status().Update(context.Background(), nc); err != nil {
			t.Fatal(err)
		}
		for range 4 {
			tick()
		}
		if journalState(t, a).Operation != nil || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || q.tagDeletes != expected {
			t.Fatal("public resealing bypassed private tombstone MAC")
		}
		return true
	}
	if q.mode == "membership-prune-settled-device" {
		q.changed = true
	}
	if q.recreating() {
		return q.qualifyRecreation(t, a, c, tick, recovery, tags, model, cohort)
	}
	if q.mode == "membership-prune-settled-reappear" {
		before := state.DeepCopy().ActionApplications
		if q.mode == "membership-prune-settled-reappear" {
			restored := q.pruned[0]
			tags[restored.kind] = append(tags[restored.kind], restored.row)
		}
		for range 4 {
			tick()
		}
		after := journalState(t, a)
		if after.Operation != nil || !reflect.DeepEqual(before, after.ActionApplications) || q.tagDeletes != expected || tagWrites != q.pruneWrites || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("resurrection borrowed retained deletion authority")
		}
		return true
	}
	for range 3 {
		tick()
	}
	if journalState(t, a).Operation != nil || c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || q.tagDeletes != expected {
		t.Fatal("prune no-op replayed")
	}
	return true
}
func TestWirelessTagPrune(t *testing.T) {
	for _, cohort := range []bool{false, true} {
		for _, mode := range wirelessTagPruneModes {
			t.Run(fmt.Sprintf("cohort-%t/%s", cohort, mode), func(t *testing.T) { exerciseWirelessTagReceipts(t, cohort, mode, nil) })
		}
	}
}
