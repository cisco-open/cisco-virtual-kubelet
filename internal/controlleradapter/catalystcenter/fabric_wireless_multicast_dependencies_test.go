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

func TestFabricWirelessMulticastDependencies(t *testing.T) {
	for _, mode := range []string{"valid", "no-wlc", "no-ssid", "unknown-mapping", "changed-wlc", "changed-tag", "changed-setting"} {
		t.Run(mode, func(t *testing.T) {
			revised := false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected mutation")
					w.WriteHeader(400)
					return
				}
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				switch r.URL.Path {
				case sitesPath:
					reply(sampleSites())
				case fabricSitesPath:
					b := false
					reply([]fabricSite{{ID: "fabric", SiteID: "area", Authentication: "No Authentication", PubSub: &b}})
				case "/dna/intent/api/v1/sda/fabrics/fabric/wirelessMulticast":
					reply(map[string]any{"multicastEnabled": mode == "changed-setting" && revised})
				case fabricDevicesPath:
					if r.URL.Query().Get("fabricId") != "fabric" {
						t.Error("unscoped memberships")
					}
					rows := []fabricDeviceMembership{{ID: "edge-member", NetworkDeviceID: "edge", FabricID: "fabric", Roles: []string{"EDGE_NODE"}}}
					if mode != "no-wlc" {
						id := "wlc-member"
						if revised && mode == "changed-wlc" {
							id = "replacement"
						}
						rows = append(rows, fabricDeviceMembership{ID: id, NetworkDeviceID: "wlc", FabricID: "fabric", Roles: []string{"WIRELESS_CONTROLLER_NODE"}})
					}
					reply(rows)
				case "/dna/intent/api/v1/sda/fabrics/fabric/switchWirelessSetting":
					reply([]fabricEWLCSetting{})
				case "/dna/intent/api/v1/sda/fabrics/fabric/vlanToSsids":
					if mode == "unknown-mapping" {
						w.WriteHeader(404)
						return
					}
					tag := "Employees"
					if revised && mode == "changed-tag" {
						tag = "Contractors"
					}
					rows := []fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Staff", SecurityGroup: &tag}}}}
					if mode == "no-ssid" {
						rows[0].SSIDs = []fabricSSIDBinding{}
					}
					reply(rows)
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			ctx := context.Background()
			before, err := c.wirelessMulticastDependencies(ctx, "Global/Lab")
			invalid := mode == "no-wlc" || mode == "no-ssid" || mode == "unknown-mapping"
			if invalid {
				if err == nil || before != nil {
					t.Fatal("missing prerequisites accepted")
				}
				return
			}
			if err != nil || len(before.members) != 2 || before.members[0].NetworkDeviceID != "edge" {
				t.Fatal("incomplete physical scope", before, err)
			}
			revised = true
			after, err := c.wirelessMulticastDependencies(ctx, "Global/Lab")
			if err != nil {
				t.Fatal(err)
			}
			if (before.digest == after.digest) != (mode == "valid") {
				t.Fatal("dependency identity change lost")
			}
		})
	}
}
