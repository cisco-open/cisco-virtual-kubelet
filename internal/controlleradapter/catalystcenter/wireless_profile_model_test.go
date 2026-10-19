// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"strings"
	"testing"
)

func wirelessProfileTestModel(row map[string]any) []byte {
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_profiles": map[string]any{"wireless": []any{row}}}})
	return raw
}
func TestWirelessProfilePinnedFieldsAndDefaults(t *testing.T) {
	row := map[string]any{
		"name": "PROFILE", "sites": []any{"Global/Lab"}, "additional_interfaces": []any{"data"}, "feature_templates": []any{"FRA", "CleanAir"},
		"ssid_details": []any{
			map[string]any{"name": "plain"},
			map[string]any{"name": "flex", "enable_fabric": false, "enable_flex_connect": true, "local_to_vlan": 100, "vlan_group_name": "VG", "wlan_profile_name": "WLAN", "dot11be_profile_name": "BE", "anchor_group_name": "ANCHOR"},
			map[string]any{"name": "fabric", "enable_fabric": true, "interface_name": "ignored", "local_to_vlan": 200},
		},
		"ap_zones":    []any{map[string]any{"name": "Zone", "rf_profile_name": "RF", "ssids": []any{"plain", "flex"}}},
		"site_tags":   []any{map[string]any{"name": "SiteTag", "sites": []any{"Global/Lab"}, "ap_profile_name": "AP"}},
		"policy_tags": []any{map[string]any{"name": "PolicyTag", "sites": []any{"Global/Lab"}, "ap_zones": []any{"Zone"}}},
	}
	profiles, err := parseWirelessProfiles(wirelessProfileTestModel(row))
	if err != nil {
		t.Fatal(err)
	}
	p := profiles[0]
	if len(p.ssids) != 3 || len(p.zones) != 1 || len(p.siteTags) != 1 || len(p.policyTags) != 1 || len(p.features) != 2 || len(p.sites) != 1 {
		t.Fatal("pinned collections lost")
	}
	byName := map[string]wirelessProfileSSID{}
	for _, ssid := range p.ssids {
		byName[ssid.name] = ssid
	}
	if byName["plain"].fields["interfaceName"] != "management" {
		t.Fatal("pinned default missing")
	}
	flex := byName["flex"]
	if flex.dot11be != "BE" || flex.fields["vlanGroupName"] != "VG" || flex.fields["anchorGroupName"] != "ANCHOR" || flex.fields["flexConnect"].(map[string]any)["localToVlan"] != int64(100) {
		t.Fatal("references or FlexConnect mapping lost")
	}
	if _, ok := flex.fields["interfaceName"]; ok {
		t.Fatal("interface default conflicts with VLAN group")
	}
	if _, ok := byName["fabric"].fields["interfaceName"]; ok {
		t.Fatal("fabric emitted interface")
	}
	if _, ok := byName["fabric"].fields["flexConnect"]; ok {
		t.Fatal("disabled FlexConnect emitted VLAN")
	}
	if p.siteTags[0].fields["flexProfileName"] != "default-flex-profile" {
		t.Fatal("site tag default lost")
	}
	if p.zones[0].fields["rfProfileName"] != "RF" || p.policyTags[0].zones[0] != "Zone" {
		t.Fatal("tag or RF reference lost")
	}
}
func TestWirelessProfileModelRejectsInvalidIntent(t *testing.T) {
	for name, extra := range map[string]map[string]any{
		"joined field names": {"name|ssid_details": true},
		"unknown":            {"future": true},
		"null":               {"sites": nil},
		"duplicate sites":    {"sites": []any{"Global/Lab", "Global/Lab"}},
		"global assignment":  {"sites": []any{"Global"}},
		"unknown ssid field": {"ssid_details": []any{map[string]any{"name": "A", "password": "secret-sentinel"}}},
		"conflicting VLAN":   {"ssid_details": []any{map[string]any{"name": "A", "interface_name": "I", "vlan_group_name": "G"}}},
		"invalid VLAN":       {"ssid_details": []any{map[string]any{"name": "A", "enable_flex_connect": true, "local_to_vlan": 4095}}},
		"wrong boolean":      {"ssid_details": []any{map[string]any{"name": "A", "enable_fabric": "true"}}},
		"duplicate SSID":     {"ssid_details": []any{map[string]any{"name": "A"}, map[string]any{"name": "A"}}},
		"wrong tag field":    {"policy_tags": []any{map[string]any{"name": "T", "flex_profile_name": "wrong"}}},
		"duplicate zone":     {"ap_zones": []any{map[string]any{"name": "Z"}, map[string]any{"name": "Z"}}},
	} {
		t.Run(name, func(t *testing.T) {
			extra["name"] = "P"
			_, err := parseWirelessProfiles(wirelessProfileTestModel(extra))
			if err == nil {
				t.Fatal("invalid model accepted")
			}
			if strings.Contains(err.Error(), "secret-sentinel") {
				t.Fatal("input leaked")
			}
		})
	}
}

func TestWirelessProfileSSIDReferencePreservesName(t *testing.T) {
	row := map[string]any{"name": "P", "ssid_details": []any{map[string]any{"name": " Lab#SSID "}}, "ap_zones": []any{map[string]any{"name": "Z", "ssids": []any{" Lab#SSID "}}}}
	p, err := parseWirelessProfiles(wirelessProfileTestModel(row))
	if err != nil || p[0].ssids[0].name != " Lab#SSID " || p[0].zones[0].ssids[0] != " Lab#SSID " {
		t.Fatal("SSID reference changed", err)
	}
}
