// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

var profileSiteTagDependencyModes = []string{"membership-compose-complete", "membership-compose-retain", "membership-compose-foreign-tag", "membership-compose-unselected-tag", "membership-compose-device", "membership-compose-read-error", "membership-compose-preflight-consumer", "membership-compose-late-foreign", "membership-compose-post-task-foreign", "membership-compose-lost-ack", "membership-compose-child-failed", "membership-compose-source", "membership-compose-owner", "membership-compose-parent-change"}

func (q *wirelessTagReceiptQualification) composition() bool {
	return strings.HasPrefix(q.mode, "membership-compose-")
}
func (q *wirelessTagReceiptQualification) compositionTagRead(t *testing.T, a *adapter, tags map[string][]map[string]any, r *http.Request) {
	if !q.composition() || q.stage != 2 {
		return
	}
	if q.mode == "membership-compose-unselected-tag" {
		for _, row := range tags["policyTags"] {
			if row["policyTagId"] == "unowned-unselected" {
				return
			}
		}
		tags["policyTags"] = append(tags["policyTags"], map[string]any{"policyTagId": "unowned-unselected", "policyTagName": "UNSELECTED", "siteIds": []any{"lab2-site"}, "apZones": []any{"default-zone"}})
		return
	}
	if q.mode == "membership-compose-late-foreign" || q.mode == "membership-compose-post-task-foreign" {
		state := journalState(t, a)
		if state.Operation != nil && (q.mode == "membership-compose-late-foreign" && state.Operation.Phase == "Submitting" || q.mode == "membership-compose-post-task-foreign" && state.Operation.TaskID != "") {
			q.changed = true
		}
	}
	if q.mode != "membership-compose-foreign-tag" && !((q.mode == "membership-compose-late-foreign" || q.mode == "membership-compose-post-task-foreign") && q.changed) {
		return
	}
	for _, row := range tags["siteTags"] {
		if row["siteTagName"] == "FOREIGN" {
			return
		}
	}
	tags["siteTags"] = append(tags["siteTags"], map[string]any{"siteTagId": "foreign-tag", "siteTagName": "FOREIGN", "siteIds": []any{"lab2-site"}, "apProfileName": "AP", "flexProfileName": "default-flex-profile"})
}
func (q *wirelessTagReceiptQualification) compositionDelete(t *testing.T, a *adapter, w http.ResponseWriter, r *http.Request, assignments *[]map[string]string, tags map[string][]map[string]any, recovery *wirelessProfileRecoveryQualification) bool {
	if r.Method != "DELETE" || !strings.HasPrefix(r.URL.Path, profileSiteAssignmentsPath("profile-id")+"/") {
		return false
	}
	if !q.composition() || q.stage != 2 || r.URL.Path != profileSiteAssignmentsPath("profile-id")+"/lab2-site" || q.deletes != 0 {
		t.Error("association removal escaped its original candidate")
		w.WriteHeader(403)
		return true
	}
	// Native current references, rather than any prospective projection, must
	// be gone before DELETE is allowed through the actual transport.
	for _, rows := range tags {
		for _, row := range rows {
			ids, _ := row["siteIds"].([]any)
			for _, id := range ids {
				if id == "lab2-site" {
					t.Error("association removed before actual tag references")
					w.WriteHeader(409)
					return true
				}
			}
		}
	}
	kept := []map[string]string{}
	for _, row := range *assignments {
		if row["id"] != "lab2-site" {
			kept = append(kept, row)
		}
	}
	*assignments = kept
	q.deletes++
	recovery.respondTask(t, a, w, time.Now().UnixMilli())
	return true
}
func TestProfileSiteTagDependencies(t *testing.T) {
	for _, cohort := range []bool{false, true} {
		for _, mode := range profileSiteTagDependencyModes {
			t.Run(fmt.Sprintf("cohort-%t/%s", cohort, mode), func(t *testing.T) { exerciseWirelessTagReceipts(t, cohort, mode, nil) })
		}
	}
}
