// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestFabricVLANSSIDInventory(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "paged", "repeat-page", "late-error", "missing-response", "null-list", "missing-vlan", "missing-ssids", "duplicate-ssid", "conflicting-vlan", "invalid-ssid", "invalid-tag", "unknown-vlan-policy", "unknown-ssid-policy"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				reads++
				if r.Method != "GET" || r.URL.Path != "/dna/intent/api/v1/sda/fabrics/fabric/vlanToSsids" || r.URL.Query().Get("limit") != "50" {
					t.Error("incorrect request", r.URL)
					w.WriteHeader(400)
					return
				}
				offset := 1 + (reads-1)*50
				if r.URL.Query().Get("offset") != fmt.Sprint(offset) {
					t.Error("incorrect offset")
				}
				if mode == "missing-response" {
					fmt.Fprint(w, `{}`)
					return
				}
				if mode == "null-list" {
					fmt.Fprint(w, `{"response":null}`)
					return
				}
				if mode == "unknown-vlan-policy" {
					fmt.Fprint(w, `{"response":[{"vlanName":"Clients","ssidDetails":[{"name":"Staff"}],"futurePolicy":{"enabled":true}}]}`)
					return
				}
				if mode == "unknown-ssid-policy" {
					fmt.Fprint(w, `{"response":[{"vlanName":"Clients","ssidDetails":[{"name":"Staff","futurePolicy":true}]}]}`)
					return
				}
				tag := "Employees"
				rows := []fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Staff", SecurityGroup: &tag}, {Name: "Guest"}}}}
				switch mode {
				case "empty":
					rows = []fabricVLANSSID{}
				case "missing-vlan":
					rows[0].VLAN = ""
				case "missing-ssids":
					rows[0].SSIDs = nil
				case "duplicate-ssid":
					rows[0].SSIDs = append(rows[0].SSIDs, rows[0].SSIDs[0])
				case "conflicting-vlan":
					rows = append(rows, fabricVLANSSID{VLAN: "Other", SSIDs: []fabricSSIDBinding{{Name: "Staff"}}})
				case "invalid-ssid":
					rows[0].SSIDs[0].Name = "bad\n"
				case "invalid-tag":
					tag = "bad\n"
				case "paged", "repeat-page", "late-error":
					if reads == 2 && mode == "late-error" {
						w.WriteHeader(500)
						return
					}
					rows = []fabricVLANSSID{}
					start := offset
					if mode == "repeat-page" {
						start = 1
					}
					count := 50
					if reads == 2 && mode != "repeat-page" {
						count = 1
					}
					for i := start; i < start+count; i++ {
						rows = append(rows, fabricVLANSSID{VLAN: fmt.Sprintf("vlan-%03d", i), SSIDs: []fabricSSIDBinding{{Name: fmt.Sprintf("ssid-%03d", i)}}})
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			rows, err := c.fabricVLANSSIDs(context.Background(), "fabric")
			valid := mode == "valid" || mode == "empty" || mode == "paged"
			if (err == nil) != valid {
				t.Fatal("inventory", err)
			}
			if !valid {
				if rows != nil {
					t.Fatal("partial inventory escaped")
				}
				return
			}
			if mode == "paged" && (len(rows) != 51 || reads != 2) {
				t.Fatal("pagination", len(rows), reads)
			}
			if mode == "empty" && len(rows) != 0 {
				t.Fatal("nonempty inventory")
			}
			if mode == "valid" && (len(rows) != 1 || rows[0].SSIDs[0].Name != "Guest" || rows[0].SSIDs[0].SecurityGroup != nil || *rows[0].SSIDs[1].SecurityGroup != "Employees") {
				t.Fatal("policy or ordering lost", rows)
			}
		})
	}
}
