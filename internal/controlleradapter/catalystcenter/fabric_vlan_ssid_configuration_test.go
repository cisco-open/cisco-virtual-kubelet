// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestFabricVLANSSIDCompilerReport(t *testing.T) {
	for _, mode := range []string{"match", "missing", "wrong-vlan", "unrelated", "empty", "mixed-multicast", "changed-tag", "changed-fabric", "late-error", "missing-fabric", "malformed", "unknown-model", "unknown-native-policy"} {
		t.Run(mode, func(t *testing.T) {
			ssids := []any{map[string]any{"name": "Staff", "vlan_name": "Clients"}}
			if mode == "empty" {
				ssids = []any{}
			}
			site := map[string]any{"name": "Global/Lab", "pub_sub_enabled": false, "authentication_template": map[string]any{"name": "No Authentication"}, "wireless_ssids": ssids}
			if mode == "mixed-multicast" {
				site["multicast"] = map[string]any{"wireless_multicast_enabled": true}
			}
			if mode == "unknown-model" {
				site["unconsumed"] = true
			}
			raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"fabric": map[string]any{"fabric_sites": []any{site}}}})
			reads, calls := 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				calls++
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
					id := "fabric"
					if mode == "changed-fabric" && reads > 0 {
						id = "replacement"
					}
					rows := []fabricSite{{ID: id, SiteID: "area", Authentication: "No Authentication", PubSub: &b}}
					if mode == "missing-fabric" {
						rows = nil
					}
					reply(rows)
				case "/dna/intent/api/v1/sda/fabrics/fabric/wirelessMulticast":
					reply(map[string]bool{"multicastEnabled": false})
				case "/dna/intent/api/v1/sda/fabrics/fabric/vlanToSsids", "/dna/intent/api/v1/sda/fabrics/replacement/vlanToSsids":
					reads++
					if mode == "late-error" && reads > 1 {
						w.WriteHeader(500)
						return
					}
					if mode == "unknown-native-policy" {
						fmt.Fprint(w, `{"response":[{"vlanName":"Clients","ssidDetails":[{"name":"Staff","extraPolicy":"retain"}]}]}`)
						return
					}
					if mode == "malformed" {
						reply(nil)
						return
					}
					vlan, tag := "Clients", "Employees"
					if mode == "wrong-vlan" {
						vlan = "Other"
					}
					if mode == "changed-tag" && reads > 1 {
						tag = "Changed"
					}
					rows := []fabricVLANSSID{{VLAN: vlan, SSIDs: []fabricSSIDBinding{{Name: "Staff", SecurityGroup: &tag}}}}
					if mode == "missing" {
						rows = []fabricVLANSSID{}
					}
					if mode == "unrelated" {
						rows = append(rows, fabricVLANSSID{VLAN: "Guest", SSIDs: []fabricSSIDBinding{{Name: "Guest"}}})
					}
					reply(rows)
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			plan, err := c.compileFabricConfiguration(raw)
			if mode == "unknown-model" {
				if err == nil || plan != nil || calls != 0 {
					t.Fatal("unconsumed model admitted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			count := 2
			if mode == "empty" {
				count = 1
			}
			if mode == "mixed-multicast" {
				count = 3
			}
			if plan.count() != count {
				t.Fatal("count", plan.count())
			}
			if mode != "empty" {
				write, err := plan.next(context.Background())
				if !errors.Is(err, errConfigurationDeviceAdmission) || write != nil || calls != 0 {
					t.Fatal("Apply gate bypassed", err)
				}
				verified, err := plan.verify(context.Background(), nil)
				if verified || !errors.Is(err, errConfigurationDeviceAdmission) {
					t.Fatal("verify gate bypassed", err)
				}
			}
			drift, err := plan.report(context.Background())
			failed := mode == "changed-tag" || mode == "changed-fabric" || mode == "late-error" || mode == "missing-fabric" || mode == "malformed" || mode == "unknown-native-policy"
			if failed {
				if err == nil || drift != nil {
					t.Fatal("partial or unknown report", drift, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := 0
			if mode == "missing" || mode == "wrong-vlan" || mode == "mixed-multicast" {
				expected = 1
			}
			if len(drift) != expected {
				t.Fatal("drift", drift)
			}
			if mode == "missing" && string(drift[0].Change) != "Add" {
				t.Fatal("missing mapping change")
			}
			if mode == "wrong-vlan" && string(drift[0].Change) != "Update" {
				t.Fatal("wrong mapping change")
			}
			if mode == "empty" && reads != 0 {
				t.Fatal("empty declaration read mappings")
			}
		})
	}
}
