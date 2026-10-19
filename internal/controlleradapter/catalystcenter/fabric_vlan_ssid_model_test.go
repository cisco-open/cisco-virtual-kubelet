// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestFabricVLANSSIDModel(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"grouped", `[{"name":"Staff","vlan_name":"Clients"},{"name":"Guest","vlan_name":"Clients"},{"name":"IoT","vlan_name":"Devices"}]`, true},
		{"empty", `[]`, true},
		{"null", `null`, false},
		{"missing-vlan", `[{"name":"Staff"}]`, false},
		{"missing-name", `[{"vlan_name":"Clients"}]`, false},
		{"tag-injection", `[{"name":"Staff","vlan_name":"Clients","security_group_tag":"Admin"}]`, false},
		{"duplicate", `[{"name":"Staff","vlan_name":"Clients"},{"name":"Staff","vlan_name":"Clients"}]`, false},
		{"conflict", `[{"name":"Staff","vlan_name":"Clients"},{"name":"Staff","vlan_name":"Devices"}]`, false},
		{"case-sensitive", `[{"name":"Staff","vlan_name":"Clients"},{"name":"staff","vlan_name":"Devices"}]`, true},
		{"bad-name", `[{"name":"Staff\n","vlan_name":"Clients"}]`, false},
		{"bad-type", `[{"name":"Staff","vlan_name":10}]`, false},
		{"long-name", `[{"name":"` + strings.Repeat("a", 33) + `","vlan_name":"Clients"}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(tc.raw), &value); err != nil {
				t.Fatal(err)
			}
			got, err := parseFabricWirelessSSIDs(value)
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
			if !tc.valid && got != nil {
				t.Fatal("partial intent")
			}
			if tc.name == "grouped" {
				expected := []fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Guest"}, {Name: "Staff"}}}, {VLAN: "Devices", SSIDs: []fabricSSIDBinding{{Name: "IoT"}}}}
				if !reflect.DeepEqual(got, expected) {
					t.Fatal("incorrect grouping", got)
				}
			}
		})
	}
}

func TestFabricVLANSSIDSiteComposition(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "multicast", "unknown", "reconfigure", "invalid-base", "duplicate-site"} {
		t.Run(mode, func(t *testing.T) {
			site := map[string]any{"name": "Global/Lab", "pub_sub_enabled": false, "authentication_template": map[string]any{"name": "No Authentication"}, "wireless_ssids": []any{map[string]any{"name": "Staff", "vlan_name": "Clients"}}}
			rows := []any{site}
			switch mode {
			case "empty":
				site["wireless_ssids"] = []any{}
			case "multicast":
				site["multicast"] = map[string]any{"wireless_multicast_enabled": true}
			case "unknown":
				site["nativeId"] = "injected"
			case "reconfigure":
				site["reconfigure"] = true
			case "invalid-base":
				site["pub_sub_enabled"] = "false"
			case "duplicate-site":
				rows = append(rows, site)
			}
			before, _ := json.Marshal(rows)
			model, multicast, intents, err := parseFabricSitesWithWirelessSSIDs(rows)
			after, _ := json.Marshal(rows)
			if string(before) != string(after) {
				t.Fatal("model mutated")
			}
			valid := mode == "valid" || mode == "empty" || mode == "multicast"
			if (err == nil) != valid {
				t.Fatal("composition", err)
			}
			if !valid {
				return
			}
			count := 1
			if mode == "empty" {
				count = 0
			}
			if len(model.sites) != 1 || len(intents) != count {
				t.Fatal("lost intent")
			}
			if mode == "multicast" && (len(multicast) != 1 || multicast[0].intent.wireless == nil || !*multicast[0].intent.wireless) {
				t.Fatal("multicast discarded")
			}
		})
	}
}
