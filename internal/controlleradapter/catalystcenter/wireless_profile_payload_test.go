// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"testing"
)

func wirelessProfilePayloadFixture(t *testing.T) (wirelessProfileIntent, wirelessProfileReferences, *wirelessProfileMetadata) {
	t.Helper()
	profiles, err := parseWirelessProfiles(wirelessProfileTestModel(map[string]any{
		"name": "P", "ssid_details": []any{map[string]any{"name": "SSID", "dot11be_profile_name": "BE", "enable_flex_connect": false}},
		"ap_zones":          []any{map[string]any{"name": "Z", "rf_profile_name": "RF", "ssids": []any{"SSID"}}},
		"feature_templates": []any{"FRA"}, "additional_interfaces": []any{"data"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	refs := wirelessProfileReferences{global: "global", ssids: map[string]wirelessSSIDMetadata{"SSID": {id: "ssid-id", name: "SSID"}}, interfaces: map[string]string{"management": "management-id", "data": "data-id"}, dot11be: map[string]string{"BE": "be-id"}, anchors: map[string]string{"A": "anchor-id"}, rf: map[string]string{"RF": "rf-id"}, features: map[string]string{"FRA": "fra-id"}}
	actual := &wirelessProfileMetadata{id: "profile-id", name: "P", fields: map[string]any{"wirelessProfileName": "P", "ssidDetails": []map[string]any{{"ssidName": "SSID", "enableFabric": false, "wlanProfileName": "retained-wlan", "anchorGroupName": "A", "vlanGroupName": "old-vlan", "flexConnect": map[string]any{"enableFlexConnect": true, "localToVlan": int64(100)}}}}}
	return profiles[0], refs, actual
}
func TestWirelessProfilePayloadReferencesAndPreservation(t *testing.T) {
	want, refs, actual := wirelessProfilePayloadFixture(t)
	before, _ := json.Marshal(actual.fields)
	out, err := want.payload(refs, actual)
	if err != nil {
		t.Fatal(err)
	}
	ssid := out["ssidDetails"].([]map[string]any)[0]
	if ssid["dot11beProfileId"] != "be-id" || ssid["interfaceName"] != "management" || ssid["wlanProfileName"] != "retained-wlan" || ssid["anchorGroupName"] != "A" {
		t.Fatal("binding or retained metadata lost")
	}
	if _, ok := ssid["vlanGroupName"]; ok {
		t.Fatal("old VLAN group conflicts with interface")
	}
	if _, ok := ssid["flexConnect"].(map[string]any)["localToVlan"]; ok {
		t.Fatal("disabled FlexConnect retained VLAN")
	}
	feature := out["featureTemplates"].([]map[string]any)[0]
	if feature["id"] != "fra-id" || len(feature["ssids"].([]string)) != 0 {
		t.Fatal("feature mapping lost")
	}
	after, _ := json.Marshal(actual.fields)
	if string(before) != string(after) {
		t.Fatal("observed state mutated")
	}
	ssid["wlanProfileName"] = "changed"
	if want.ssids[0].fields["wlanProfileName"] != nil {
		t.Fatal("intent aliased")
	}
}
func TestWirelessProfilePayloadRejectsMissingReferences(t *testing.T) {
	for _, kind := range []string{"ssid", "interface", "additional-interface", "dot11be", "anchor", "rf", "feature", "foreign-site", "sensor", "unknown-field", "replacement-profile"} {
		t.Run(kind, func(t *testing.T) {
			want, refs, actual := wirelessProfilePayloadFixture(t)
			switch kind {
			case "ssid":
				delete(refs.ssids, "SSID")
			case "interface":
				delete(refs.interfaces, "management")
			case "additional-interface":
				delete(refs.interfaces, "data")
			case "dot11be":
				delete(refs.dot11be, "BE")
			case "anchor":
				delete(refs.anchors, "A")
			case "rf":
				delete(refs.rf, "RF")
			case "feature":
				delete(refs.features, "FRA")
			case "foreign-site":
				entry := refs.ssids["SSID"]
				entry.inheritedSiteID = "foreign"
				refs.ssids["SSID"] = entry
			case "sensor":
				entry := refs.ssids["SSID"]
				entry.sensor = true
				refs.ssids["SSID"] = entry
			case "unknown-field":
				actual.unmapped = true
			case "replacement-profile":
				actual.name = "replacement"
			}
			if _, err := want.payload(refs, actual); err == nil {
				t.Fatal("unsafe reference accepted")
			}
		})
	}
}
func TestWirelessProfilePayloadRetainedDependencyAndRemoval(t *testing.T) {
	want, refs, actual := wirelessProfilePayloadFixture(t)
	want.features = nil
	actual.fields["featureTemplates"] = []map[string]any{{"id": "missing-feature", "ssids": []string{}}}
	if _, err := want.payload(refs, actual); err == nil {
		t.Fatal("retained unresolved feature accepted")
	}
	want.features = []string{}
	out, err := want.payload(refs, actual)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out["featureTemplates"], []map[string]any{}) {
		t.Fatal("explicit empty collection lost")
	}
	want.ssids = []wirelessProfileSSID{}
	if _, err := want.payload(refs, actual); err == nil {
		t.Fatal("zone reference to removed SSID accepted")
	}
}
