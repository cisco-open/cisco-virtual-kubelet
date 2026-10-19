// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFabricVLANSSIDSelection(t *testing.T) {
	for _, mode := range []string{"add", "match", "move-owned", "move-unowned", "stale-vlan", "stale-tag", "empty", "invalid-observed", "injected-tag"} {
		t.Run(mode, func(t *testing.T) {
			tag := "Employees"
			before := []fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Staff", SecurityGroup: &tag}, {Name: "Guest"}}}, {VLAN: "Empty", SSIDs: []fabricSSIDBinding{}}}
			want := []fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "New"}}}}
			owned := map[string]fabricVLANSSIDOwned{}
			switch mode {
			case "match":
				want[0].SSIDs[0].Name = "Staff"
			case "move-owned", "move-unowned", "stale-vlan", "stale-tag":
				want[0].VLAN = "Other"
				want[0].SSIDs[0].Name = "Staff"
				if mode != "move-unowned" {
					copy := tag
					owned["Staff"] = fabricVLANSSIDOwned{VLAN: "Clients", SecurityGroup: &copy}
				}
				if mode == "stale-vlan" {
					row := owned["Staff"]
					row.VLAN = "Old"
					owned["Staff"] = row
				}
				if mode == "stale-tag" {
					old := "Old"
					owned["Staff"] = fabricVLANSSIDOwned{VLAN: "Clients", SecurityGroup: &old}
				}
			case "empty":
				want = []fabricVLANSSID{}
			case "invalid-observed":
				before = append(before, before[0])
			case "injected-tag":
				want[0].SSIDs[0].SecurityGroup = &tag
			}
			original, _ := json.Marshal([]any{before, want, owned})
			selected, err := selectFabricVLANSSIDs(want, before, owned)
			after, _ := json.Marshal([]any{before, want, owned})
			if string(original) != string(after) {
				t.Fatal("caller data changed")
			}
			failed := mode == "move-unowned" || mode == "stale-vlan" || mode == "stale-tag" || mode == "invalid-observed" || mode == "injected-tag"
			if failed {
				if err == nil || selected != nil {
					t.Fatal("unsafe selection accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := canonicalFabricVLANSSIDs(before)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "match" || mode == "empty" {
				if !reflect.DeepEqual(selected.payload, canonical) || len(selected.added)+len(selected.moved) != 0 {
					t.Fatal("implicit adoption/deletion")
				}
				return
			}
			byName := map[string]string{}
			groups := map[string]*string{}
			emptyRetained := false
			for _, row := range selected.payload {
				if row.VLAN == "Empty" && row.SSIDs != nil && len(row.SSIDs) == 0 {
					emptyRetained = true
				}
				for _, ssid := range row.SSIDs {
					byName[ssid.Name] = row.VLAN
					groups[ssid.Name] = ssid.SecurityGroup
				}
			}
			if !emptyRetained || byName["Guest"] != "Clients" || groups["Staff"] == nil || *groups["Staff"] != "Employees" {
				t.Fatal("existing policy lost", selected)
			}
			if mode == "add" && (!reflect.DeepEqual(selected.added, []string{"New"}) || byName["Staff"] != "Clients" || byName["New"] != "Clients") {
				t.Fatal("addition incorrect")
			}
			if mode == "move-owned" && (!reflect.DeepEqual(selected.moved, []string{"Staff"}) || byName["Staff"] != "Other") {
				t.Fatal("owned move incorrect")
			}
			*groups["Staff"] = "Changed"
			if tag != "Employees" {
				t.Fatal("output aliases native policy")
			}
		})
	}
}
