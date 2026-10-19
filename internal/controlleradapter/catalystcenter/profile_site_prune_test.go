// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

var profileSitePruneModes = []string{"complete", "unknown", "import", "retain", "empty-source", "capacity", "recreate", "absent-before", "site-replaced", "parent-replaced", "rename", "preflight-refusal", "preflight-identity", "lost-ack", "source-change", "owner-change", "history-change", "child-failed", "tree-missing", "drop-verified", "edit-verified", "edit-settled", "reappear", "device", "child-device", "device-read-error", "task-site-replaced", "task-parent-replaced", "task-present", "mid-tree-site", "mid-tree-history", "pnp", "child-pnp", "pnp-location", "pnp-unbound", "pnp-read-error", "pnp-unknown-site", "pnp-unclaimed"}
var profileSiteWirelessPruneModes = []string{"site-tag", "policy-tag", "child-tag", "unknown-tag", "tag-read-error", "post-task-tag"}

type profileSitePruneQualification struct {
	mode                  string
	a                     *adapter
	stage, steps, deletes int
	block                 bool
	injected              bool
	held                  *cvk.NetworkControllerConfigurationOperation
	removedID             string
	siteID                *string
	mutated               bool
}

func (q *profileSitePruneQualification) handle(t *testing.T, w http.ResponseWriter, r *http.Request, assigned map[string]bool, writes *int, stamps map[string]int64) bool {
	t.Helper()
	if q.a != nil && q.stage > 0 && r.Method == http.MethodGet && (r.URL.Path == networkProfilesPath || r.URL.Path == wirelessProfilesPath) && !q.injected && (q.mode == "preflight-refusal" || q.mode == "preflight-identity") {
		state := journalState(t, q.a)
		if state.Operation != nil && state.Operation.Action == "Delete" && state.Operation.Phase == "Submitting" {
			q.injected = true
			if q.mode == "preflight-refusal" {
				w.WriteHeader(503)
				return true
			}
			// Replacing the UUID during native preflight must certify no DELETE.
			*q.siteID = "replacement-site"
			return false
		}
	}
	if r.URL.Path == pnpDevicesPath {
		if q.block && q.mode == "pnp-read-error" {
			w.WriteHeader(503)
			return true
		}
		rows := []map[string]any{}
		if q.block && strings.Contains(q.mode, "pnp") {
			info := map[string]any{"serialNumber": "FOC12345678", "pid": "C9300", "state": "Claimed", "onbState": "Planned", "siteId": "site-a"}
			switch q.mode {
			case "child-pnp":
				info["siteId"] = "child-site"
			case "pnp-location":
				delete(info, "siteId")
				info["location"] = map[string]string{"siteId": "site-a"}
			case "pnp-unknown-site":
				info["siteId"] = "unknown-site"
			case "pnp-unbound":
				delete(info, "siteId")
				info["workflowId"] = "workflow"
			case "pnp-unclaimed":
				delete(info, "siteId")
				info["state"] = "Unclaimed"
				info["onbState"] = "Not Contacted"
			}
			rows = append(rows, map[string]any{"id": "pnp-device", "deviceInfo": info})
		}
		json.NewEncoder(w).Encode(rows)
		return true
	}
	if r.URL.Path == assignedDevicesPath {
		if q.block && q.mode == "device-read-error" {
			w.WriteHeader(503)
			return true
		}
		rows := []map[string]string{}
		id := "site-a"
		if q.mode == "child-device" {
			id = "child-site"
		}
		if q.block && (q.mode == "device" || q.mode == "child-device") && r.URL.Query().Get("siteId") == id {
			rows = append(rows, map[string]string{"deviceId": "consumer", "siteId": id})
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
		return true
	}
	for _, policy := range []bool{false, true} {
		if r.URL.Path != wirelessProfileTagPath("profile-id", policy) {
			continue
		}
		if q.block && q.mode == "tag-read-error" {
			w.WriteHeader(503)
			return true
		}
		rows := []map[string]any{}
		if q.block && (q.mode == "site-tag" && !policy || q.mode == "policy-tag" && policy || q.mode == "child-tag" && !policy || (q.mode == "unknown-tag" || q.mode == "post-task-tag") && !policy) {
			idKey, nameKey := "siteTagId", "siteTagName"
			if policy {
				idKey, nameKey = "policyTagId", "policyTagName"
			}
			site := "site-a"
			if q.mode == "child-tag" {
				site = "child-site"
			}
			row := map[string]any{idKey: "tag-id", nameKey: "consumer", "siteIds": []string{site}}
			if policy {
				row["apZones"] = []string{"default-zone"}
			}
			if q.mode == "unknown-tag" {
				row["unqualifiedReference"] = true
			}
			rows = append(rows, row)
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
		return true
	}
	if r.Method == http.MethodDelete {
		if r.URL.Path != profileSiteAssignmentsPath("profile-id")+"/site-a" {
			t.Error("removal escaped exact original association")
			w.WriteHeader(403)
			return true
		}
		q.deletes++
		*writes++
		delete(assigned, "site-a")
		id := fmt.Sprintf("task-%d", *writes)
		stamps[id] = time.Now().UnixMilli()
		if q.mode == "lost-ack" {
			w.WriteHeader(503)
			return true
		}
		fmt.Fprintf(w, `{"response":{"taskId":%q}}`, id)
		return true
	}
	if q.deletes > 0 && strings.HasPrefix(r.URL.Path, taskPath) && strings.HasSuffix(r.URL.Path, "/tree") {
		if (q.mode == "mid-tree-site" || q.mode == "mid-tree-history") && !q.mutated {
			q.mutated = true
			if q.mode == "mid-tree-site" {
				*q.siteID = "replacement-site"
			} else {
				nc, _ := q.a.currentController(r.Context())
				nc.Status.Configuration.Owners = append(nc.Status.Configuration.Owners, cvk.NetworkControllerSectionOwner{Section: "templates", ConfigName: "other", ConfigUID: "other"})
				if err := q.a.statusWriter.Status().Update(r.Context(), nc); err != nil {
					t.Error(err)
				}
			}
		}

		if q.mode == "tree-missing" {
			w.WriteHeader(404)
			return true
		}
		if q.mode == "child-failed" {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			stamp := stamps[id]
			writeDefinitionTaskFixture(w, r, map[string]any{"id": id, "isError": false, "startTime": stamp, "endTime": stamp}, map[string]any{"id": "child", "parentId": id, "rootId": id, "isError": true, "startTime": stamp, "endTime": stamp})
			return true
		}
	}
	return false
}
func (q *profileSitePruneQualification) update(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig) {
	t.Helper()
	config.Generation++
	if err := a.statusWriter.Update(context.Background(), config); err != nil {
		t.Fatal(err)
	}
}
func (q *profileSitePruneQualification) observe(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, tick func(), assigned map[string]bool, writes int, namespace, name string, model func([]string) []byte, nativePath, profileID, siteID *string) bool {
	t.Helper()
	q.a = a
	q.siteID = siteID
	q.steps++
	ctx := context.Background()
	state := journalState(t, a)
	if q.held != nil {
		for range 4 {
			tick()
		}
		current := journalState(t, a)
		if !reflect.DeepEqual(current.Operation, q.held) || q.deletes != 1 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("uncertain removal was released or replayed")
		}
		if q.mode == "post-task-tag" {
			q.block = false
			q.held = nil
			q.mode = "unknown"
			return false
		}
		return true
	}
	if q.stage == 0 {
		if state == nil || state.Operation != nil || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
			return false
		}
		rows, err := profileSiteApplicationRecords(state)
		expected := 1
		if q.mode == "import" {
			expected = 0
		}
		if err != nil || len(rows) != expected || writes != expected {
			t.Fatal("initial application/import evidence invalid", err)
		}
		q.stage = 1
		q.steps = 0
		q.block = q.mode != "post-task-tag"
		config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
		if q.mode == "retain" {
			config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
		}
		config.Spec.Source.Inline.Raw = model(nil)
		if q.mode == "empty-source" {
			kind := "switching"
			if namespace == "wireless-sites/" {
				kind = "wireless"
			}
			config.Spec.Source.Inline.Raw = []byte(fmt.Sprintf(`{"catalyst_center":{"network_profiles":{%q:[]}}}`, kind))
		}
		q.update(t, a, config)
		switch q.mode {
		case "absent-before":
			delete(assigned, "site-a")
		case "site-replaced":
			*siteID = "replacement-site"
		case "parent-replaced":
			*profileID = "replacement-profile"
		case "rename":
			*nativePath = "Global/Renamed"
		}
		return false
	}
	if q.stage == 1 {
		noDelete := q.mode == "import" || q.mode == "retain" || q.mode == "absent-before" || q.mode == "site-replaced" || q.mode == "parent-replaced" || q.mode == "rename"
		if noDelete {
			if q.steps < 5 {
				return false
			}
			inSync := q.mode == "import" || q.mode == "retain"
			if q.deletes != 0 || state.Operation != nil || (config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync) != inSync || !assigned["foreign-site"] {
				t.Fatal("unowned, retained or changed identity was removed")
			}
			return true
		}
		blocked := (strings.Contains(q.mode, "pnp") && q.mode != "pnp-unclaimed") || q.mode == "device" || q.mode == "child-device" || q.mode == "device-read-error" || (strings.Contains(q.mode, "tag") && q.mode != "post-task-tag")
		if blocked && q.block {
			if q.steps < 4 {
				return false
			}
			if q.deletes != 0 || state.Operation != nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("referenced removal submitted")
			}
			found := false
			for _, condition := range config.Status.Conditions {
				if condition.Reason == "ProfileSitePruneReferenced" {
					found = true
				}
			}
			if !found {
				t.Fatal("missing actionable reference refusal")
			}
			q.block = false
		}
		if q.mode == "preflight-identity" && q.injected && state.Operation == nil {
			*siteID = "replacement-site"
			q.mode = "site-replaced"
			q.steps = 0
			return false
		}
		if op := state.Operation; op != nil && op.Action == "Delete" && (op.Phase == "Accepted" || op.Phase == "Unknown") {
			if q.mode == "lost-ack" {
				if op.Phase != "Unknown" || op.TaskID != "" {
					t.Fatal("lost delete acknowledgement escaped uncertainty")
				}
				q.held = op.DeepCopy()
				return false
			}
			if op.Phase == "Accepted" && q.mode != "complete" && q.mode != "empty-source" && q.mode != "capacity" && q.mode != "recreate" {
				nc, _ := a.currentController(ctx)
				switch q.mode {
				case "task-site-replaced":
					*siteID = "replacement-site"
				case "task-parent-replaced":
					*profileID = "replacement-profile"
				case "task-present":
					assigned["site-a"] = true
				case "post-task-tag":
					q.block = true
				case "source-change":
					config.Spec.Source.Inline.Raw = model([]string{"Global/A"})
					q.update(t, a, config)
				case "owner-change":
					nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Fatal(err)
					}
				case "history-change":
					nc.Status.Configuration.Owners = append(nc.Status.Configuration.Owners, cvk.NetworkControllerSectionOwner{Section: "templates", ConfigName: "other", ConfigUID: "other"})
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Fatal(err)
					}
				}
				if err := a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
				switch q.mode {
				case "source-change", "owner-change", "history-change", "child-failed", "tree-missing", "task-site-replaced", "task-parent-replaced", "task-present", "mid-tree-site", "mid-tree-history", "post-task-tag":
					q.held = journalState(t, a).Operation.DeepCopy()
				}
				return false
			}
		}
		if op := state.Operation; op != nil && op.Action == "Delete" && op.Phase == "Verified" && (q.mode == "drop-verified" || q.mode == "edit-verified") {
			if !profileSiteApplicationPresent(state, op) {
				t.Fatal("missing atomic removal receipt")
			}
			nc, _ := a.currentController(ctx)
			q.held = op.DeepCopy()
			if q.mode == "drop-verified" {
				nc.Status.Configuration.ActionApplications = nil
			} else {
				nc.Status.Configuration.ActionApplications[0].Operation.TaskID = "edited"
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			return false
		}
		if q.deletes == 0 || state.Operation != nil || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
			return false
		}
		rows, err := profileSiteApplicationRecords(state)
		if err != nil || len(rows) != 1 {
			t.Fatal("missing bounded removal history", err)
		}
		for _, row := range rows {
			if row.Operation.Action != "Delete" || row.Operation.ParentID != "profile-id" || row.Operation.TargetID != "site-a" || !strings.HasPrefix(row.Resource, profileSiteRemovedPrefix) {
				t.Fatal("incorrect removal provenance")
			}
			q.removedID = row.Operation.ID
		}
		if q.deletes != 1 || assigned["site-a"] || !assigned["foreign-site"] {
			t.Fatal("removal effects or foreign association changed")
		}
		if q.mode == "capacity" && len(state.ActionApplications) != 256 {
			t.Fatal("delete did not replace bounded application slot")
		}
		if q.mode == "preflight-refusal" && !q.injected {
			t.Fatal("unsent refusal was not exercised")
		}
		if q.mode == "recreate" {
			config.Spec.Source.Inline.Raw = model([]string{"Global/A"})
			q.update(t, a, config)
			q.stage = 2
			return false
		}
		if q.mode == "edit-settled" {
			nc, _ := a.currentController(ctx)
			nc.Status.Configuration.ActionApplications[0].Operation.TaskID = "edited-after-settlement"
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
		}
		if q.mode == "reappear" {
			assigned["site-a"] = true
		}
		for range 4 {
			tick()
		}
		if q.deletes != 1 || journalState(t, a).Operation != nil || (config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync) == (q.mode == "edit-settled" || q.mode == "reappear") {
			t.Fatal("removed history edited, replayed or invalid no-op")
		}
		return true
	}
	if q.stage == 2 {
		if state.Operation != nil || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
			return false
		}
		rows, err := profileSiteApplicationRecords(state)
		if err != nil || len(rows) != 1 || writes != 3 || q.deletes != 1 || !assigned["site-a"] || !assigned["foreign-site"] {
			t.Fatal("association re-add did not reuse bounded history", err)
		}
		for _, row := range rows {
			if row.Operation.Action != "Update" || row.Operation.ID == q.removedID {
				t.Fatal("re-add retained stale removal authority")
			}
		}
		return true
	}
	return false
}
func exerciseProfileSitePrune(t *testing.T, wireless bool, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	qualification := &profileSiteReceiptQualification{mode: "prune", prune: &profileSitePruneQualification{mode: mode}}
	if mode == "import" {
		qualification.mode = "import"
	}
	if mode == "capacity" {
		qualification.mode = "prune-at-capacity"
	}
	exerciseProfileSiteRecovery(t, wireless, "complete", install, qualification)
}
func TestProfileSitePrune(t *testing.T) {
	for _, wireless := range []bool{false, true} {
		modes := append([]string{}, profileSitePruneModes...)
		if wireless {
			modes = append(modes, profileSiteWirelessPruneModes...)
		}
		for _, mode := range modes {
			t.Run(fmt.Sprintf("wireless-%t/%s", wireless, mode), func(t *testing.T) { exerciseProfileSitePrune(t, wireless, mode, nil) })
		}
	}
}
