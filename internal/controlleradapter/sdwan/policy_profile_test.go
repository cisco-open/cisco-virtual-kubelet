// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"encoding/json"
	"testing"
)

func TestPolicyProfileFixture(t *testing.T) {
	resources := parcelFixtures(t, "policy-profile")
	if len(resources) != 16 {
		t.Fatal("expected profile and fifteen policy parcels")
	}
	if resources[0].Kind != "profile" {
		t.Fatal("profile must precede parcels")
	}
}
func TestPolicyProfileValidation(t *testing.T) {
	for _, input := range []string{
		`{"name":"CVK", "unknown":[]}`,
		`{"name":"CVK", "security_url_allow_lists":[{"name":"same","urls":["example.com"]}], "security_url_block_lists":[{"name":"same","urls":["example.org"]}]}`,
		`{"name":"CVK", "color_lists":[{"name":"colors","colors":["wrong"]}]}`,
		`{"name":"CVK", "policers":[{"name":"policer","rate_bps":1}]}`,
		`{"name":"CVK", "tloc_lists":[{"name":"tloc","tlocs":[{"color":"blue"}]}]}`,
	} {
		if _, err := compilePolicyObjectProfile(json.RawMessage(input)); err == nil {
			t.Fatal("invalid policy profile accepted")
		}
	}
}
func TestLivePolicyProfileNamesAbsent(t *testing.T) {
	liveParcelNamesAbsent(t, parcelFixtures(t, "policy-profile"))
}
func TestLivePolicyProfileLifecycle(t *testing.T) {
	liveParcelLifecycle(t, parcelFixtures(t, "policy-profile"))
}

func TestPolicyProfileNumericModelEncoding(t *testing.T) {
	for _, r := range parcelFixtures(t, "policy-profile") {
		if r.Type != "policy_object_class_map" && r.Type != "policy_object_tloc_list" {
			continue
		}
		entry := r.Body["data"].(map[string]any)["entries"].([]any)[0].(map[string]any)
		field, want := "queue", "1"
		if r.Type == "policy_object_tloc_list" {
			field, want = "preference", "100"
		}
		if entry[field].(map[string]any)["value"] != want {
			t.Fatal("NaC numeric value not encoded as provider string")
		}
	}
}
