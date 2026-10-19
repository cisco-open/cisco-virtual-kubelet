// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"testing"
)

func TestWirelessProfileTagPayload(t *testing.T) {
	for _, policy := range []bool{false, true} {
		refs := wirelessProfileTagReferences{profileID: "profile", sites: map[string]string{"Global/A": "site-a", "Global/B": "site-b"}, assigned: map[string]bool{"site-a": true, "site-b": true}, zones: map[string]bool{"zone": true}, apProfiles: map[string]string{"AP": "ap-id"}, flexProfiles: map[string]string{"default-flex-profile": "flex-id"}}
		row := map[string]any{"name": "T", "sites": []any{"Global/B", "Global/A"}}
		nameKey := "siteTagName"
		if policy {
			nameKey = "policyTagName"
			row["ap_zones"] = []any{"zone"}
		} else {
			row["ap_profile_name"] = "AP"
		}
		parsed, err := parseWirelessProfileTags([]any{row}, policy)
		if err != nil {
			t.Fatal(err)
		}
		want := parsed[0]
		actual := &wirelessProfileTagMetadata{id: "tag-id", name: "T", profileID: "profile", fields: map[string]any{nameKey: "T", "siteIds": []string{"site-a"}}}
		before, _ := json.Marshal(actual.fields)
		out, err := want.payload(refs, actual, policy)
		if err != nil {
			t.Fatal(err)
		}
		sites := out["siteIds"].([]string)
		if len(sites) != 2 || sites[0] != "site-a" || sites[1] != "site-b" {
			t.Fatal("site resolution failed")
		}
		after, _ := json.Marshal(actual.fields)
		if string(before) != string(after) {
			t.Fatal("observed state mutated")
		}
		refs.assigned["site-b"] = false
		if _, err = want.payload(refs, actual, policy); err == nil {
			t.Fatal("unassigned site accepted")
		}
		refs.assigned["site-b"] = true
		actual.profileID = "replacement"
		if _, err = want.payload(refs, actual, policy); err == nil {
			t.Fatal("tag crossed profile identity")
		}
		actual.profileID = "profile"
		actual.unmapped = true
		if _, err = want.payload(refs, actual, policy); err == nil {
			t.Fatal("unknown fields overwritten")
		}
	}
}
func TestWirelessProfileTagRetainedReferences(t *testing.T) {
	refs := wirelessProfileTagReferences{profileID: "profile", sites: map[string]string{"Global/A": "site"}, assigned: map[string]bool{"site": true}, zones: map[string]bool{"default-zone": true}}
	want := wirelessProfileTag{name: "T", sites: []string{"Global/A"}, fields: map[string]any{"policyTagName": "T"}}
	actual := &wirelessProfileTagMetadata{id: "tag", name: "T", profileID: "profile", fields: map[string]any{"policyTagName": "T", "apZones": []string{"missing-zone"}}}
	if _, err := want.payload(refs, actual, true); err == nil {
		t.Fatal("retained missing zone accepted")
	}
	want.zones = []string{}
	out, err := want.payload(refs, actual, true)
	if err != nil || len(out["apZones"].([]string)) != 1 || out["apZones"].([]string)[0] != "default-zone" {
		t.Fatal("explicit empty zones lost", err)
	}
	want.fields = map[string]any{"siteTagName": "T"}
	actual.fields = map[string]any{"siteTagName": "T", "apProfileName": "missing-ap"}
	if _, err = want.payload(refs, actual, false); err == nil {
		t.Fatal("retained missing AP accepted")
	}
	refs.apProfiles = map[string]string{"missing-ap": "ap-id"}
	if _, err = want.payload(refs, actual, false); err != nil {
		t.Fatal(err)
	}
	refs.sites["Global/B"] = "site"
	want.sites = append(want.sites, "Global/B")
	if _, err = want.payload(refs, actual, false); err == nil {
		t.Fatal("duplicate site identity accepted")
	}
}
