// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "testing"

func TestMulticastRPPhysicalIdentity(t *testing.T) {
	for _, mode := range []string{"short", "fqdn", "ambiguous-short", "exact-and-fqdn", "missing", "missing-serial", "invalid-serial", "duplicate-serial", "duplicate-id", "retained", "replacement", "case-normalization"} {
		t.Run(mode, func(t *testing.T) {
			inventory := []Device{{ID: "a", Hostname: "border-a.lab", Serial: "SERIAL-A", PlatformID: "C9500"}, {ID: "b", Hostname: "border-b.lab", Serial: "SERIAL-B", PlatformID: "C9500"}}
			name := "border-a"
			switch mode {
			case "fqdn":
				name = "border-a.lab"
			case "ambiguous-short":
				inventory[1].Hostname = "border-a.other"
			case "exact-and-fqdn":
				inventory[1].Hostname = "border-a"
			case "missing":
				name = "absent"
			case "missing-serial":
				inventory[0].Serial = ""
			case "invalid-serial":
				inventory[0].Serial = "bad serial"
			case "duplicate-serial":
				inventory[1].Serial = "serial-a"
			case "duplicate-id":
				inventory[1].ID = "a"
			case "case-normalization":
				inventory[0].Serial = "serial-a"
			}
			want := fabricMulticastVNIntent{rps: []fabricMulticastRPIntent{{devices: []string{name}}}}
			got, err := resolveMulticastRPNames([]fabricMulticastVNIntent{want}, inventory)
			valid := mode == "short" || mode == "fqdn" || mode == "retained" || mode == "replacement" || mode == "case-normalization"
			if (err == nil) != valid {
				t.Fatal("unexpected name binding", err)
			}
			if !valid {
				if got != nil {
					t.Fatal("partial name binding")
				}
				return
			}
			if got[name] != "a" {
				t.Fatal("wrong UUID")
			}
			bound, err := bindMulticastRPDevices([]string{"b", got[name], "a"}, inventory)
			if err != nil || len(bound) != 2 || bound[0].ID != "a" || bound[0].Serial != "serial-a" || bound[1].ID != "b" {
				t.Fatal("retained physical binding", err)
			}
			if inventory[0].Serial == "serial-a" && mode != "case-normalization" {
				t.Fatal("mutated inventory")
			}
			if mode == "replacement" {
				inventory[0].Serial = "REPLACEMENT"
				refreshed, err := bindMulticastRPDevices([]string{"a"}, inventory)
				if err != nil || refreshed[0].Serial == bound[0].Serial {
					t.Fatal("physical replacement invisible", err)
				}
			}
		})
	}
}
func TestMulticastRetainedRPIdentityRefusal(t *testing.T) {
	for _, mode := range []string{"absent", "empty-serial", "duplicate-serial"} {
		t.Run(mode, func(t *testing.T) {
			inventory := []Device{{ID: "a", Serial: "serial-a"}, {ID: "b", Serial: "serial-b"}}
			id := "b"
			if mode == "absent" {
				id = "missing"
			}
			if mode == "empty-serial" {
				inventory[1].Serial = ""
			}
			if mode == "duplicate-serial" {
				inventory[1].Serial = "SERIAL-A"
			}
			got, err := bindMulticastRPDevices([]string{"a", id}, inventory)
			if err == nil || got != nil {
				t.Fatal("retained RP identity accepted", err)
			}
		})
	}
}
