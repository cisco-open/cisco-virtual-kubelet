// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

var wirelessTagMembershipModes = []string{"membership-complete", "membership-retain", "membership-missing-proof", "membership-device", "membership-child-device", "membership-device-error", "membership-pnp", "membership-pnp-error", "membership-preflight-consumer", "membership-post-task-consumer", "membership-lost-ack", "membership-child-failed", "membership-tree-missing", "membership-source", "membership-owner", "membership-zone-complete", "membership-zone-device"}

func (q *wirelessTagReceiptQualification) membershipRequest(t *testing.T, a *adapter, w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(q.mode, "membership-") || q.stage < 2 {
		return false
	}
	if r.URL.Path == assignedDevicesPath {
		if q.mode == "membership-device-error" || q.mode == "membership-compose-read-error" {
			w.WriteHeader(503)
			return true
		}
		id := "lab2-site"
		if q.mode == "membership-zone-device" {
			id = "lab-site"
		}
		if q.mode == "membership-child-device" {
			id = "child-site"
		}
		blocked := q.mode == "membership-device" || q.mode == "membership-child-device" || q.mode == "membership-zone-device" || q.mode == "membership-compose-device"
		if q.mode == "membership-preflight-consumer" || q.mode == "membership-post-task-consumer" || q.mode == "membership-compose-preflight-consumer" {
			state := journalState(t, a)
			if state.Operation != nil && ((q.mode == "membership-preflight-consumer" || q.mode == "membership-compose-preflight-consumer") && state.Operation.Phase == "Submitting" || q.mode == "membership-post-task-consumer" && state.Operation.TaskID != "") {
				q.changed = true
			}
			blocked = q.changed
		}
		rows := []map[string]string{}
		if blocked && r.URL.Query().Get("siteId") == id {
			rows = append(rows, map[string]string{"deviceId": "consumer", "siteId": id})
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
		return true
	}
	if r.URL.Path == pnpDevicesPath {
		if q.mode == "membership-pnp-error" {
			w.WriteHeader(503)
			return true
		}
		rows := []map[string]any{}
		if q.mode == "membership-pnp" {
			rows = append(rows, map[string]any{"id": "pnp-consumer", "deviceInfo": map[string]any{"serialNumber": "FOC12345678", "pid": "C9300", "state": "Claimed", "onbState": "Planned", "siteId": "lab2-site"}})
		}
		json.NewEncoder(w).Encode(rows)
		return true
	}
	return false
}
func (q *wirelessTagReceiptQualification) startMembershipRemoval(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig) {
	t.Helper()
	q.stage = 2
	q.steps = 0
	var root map[string]any
	if err := json.Unmarshal(c.Spec.Source.Inline.Raw, &root); err != nil {
		t.Fatal(err)
	}
	p := root["catalyst_center"].(map[string]any)["network_profiles"].(map[string]any)["wireless"].([]any)[0].(map[string]any)
	// Retain the parent definition and both assignments. Remove only the second
	// site from each tag, keeping every tag nonempty and UUID-bound.
	for _, kind := range []string{"site_tags", "policy_tags"} {
		for _, value := range p[kind].([]any) {
			value.(map[string]any)["sites"] = []any{"Global/Lab"}
		}
	}
	if strings.HasPrefix(q.mode, "membership-zone-") {
		p["policy_tags"].([]any)[0].(map[string]any)["ap_zones"] = []any{"Z1"}
	}
	if q.composition() {
		p["sites"] = []any{"Global/Lab"}
		if q.mode == "membership-compose-parent-change" {
			p["ssid_details"].([]any)[0].(map[string]any)["wlan_profile_name"] = "WLAN3"
		}
		if q.mode == "membership-compose-unselected-tag" {
			p["policy_tags"] = []any{}
		}
	}
	c.Spec.Source.Inline.Raw, _ = json.Marshal(root)
	if q.mode != "membership-retain" && q.mode != "membership-compose-retain" {
		c.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	}
	c.Generation++
	if err := a.statusWriter.Update(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if q.mode == "membership-missing-proof" {
		nc, err := a.currentController(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		nc.Status.Configuration.ActionApplications = nil
		if err := a.statusWriter.Status().Update(context.Background(), nc); err != nil {
			t.Fatal(err)
		}
	}
}
func TestWirelessTagMembershipRemoval(t *testing.T) {
	for _, cohort := range []bool{false, true} {
		for _, mode := range wirelessTagMembershipModes {
			t.Run(fmt.Sprintf("cohort-%t/%s", cohort, mode), func(t *testing.T) { exerciseWirelessTagReceipts(t, cohort, mode, nil) })
		}
	}
}
