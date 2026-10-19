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

func TestFabricMulticastCompilerReportAndApplyGate(t *testing.T) {
	for _, mode := range []string{"add", "update", "match", "invalid-vn", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			b := false
			calls := 0
			writes := 0
			multicast := map[string]any{"replication_mode": "NATIVE_MULTICAST", "virtual_networks": []any{map[string]any{"name": "Corp", "ip_pool_name": "access", "ipv4_ssm_ranges": []any{"232.0.0.0/8"}, "multicast_rps": []any{}}}}
			if mode == "invalid-vn" {
				multicast["virtual_networks"] = []any{map[string]any{"name": "Corp"}}
			}
			site := map[string]any{"name": "Global/Lab", "pub_sub_enabled": false, "authentication_template": map[string]any{"name": "No Authentication"}, "multicast": multicast}
			if mode == "legacy" {
				delete(site, "multicast")
			}
			raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"fabric": map[string]any{"fabric_sites": []any{site}}}})
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				calls++
				if r.Method != "GET" {
					writes++
					t.Error("unexpected write")
					w.WriteHeader(400)
					return
				}
				var value any
				switch r.URL.Path {
				case sitesPath:
					value = sampleSites()
				case fabricSitesPath:
					value = []fabricSite{{ID: "fabric", SiteID: "area", Authentication: "No Authentication", PubSub: &b}}
				case fabricL3NetworksPath:
					value = []fabricL3Network{{ID: "vn", Name: "Corp", Fabrics: []string{"fabric"}}}
				case globalPoolsPath:
					value = fixturePools()
				case sitePoolsPath:
					value = fixtureReservations()
				case devicesPath:
					value = []Device{}
				case fabricDevicesPath:
					value = []fabricDeviceMembership{}
				case fabricMulticastVNsPath:
					rows := []map[string]any{}
					if mode == "match" || mode == "update" {
						row := multicastSiteRow(t, "Corp", "access").fields
						if mode == "match" {
							row["ipv4SsmRanges"] = []string{"232.0.0.0/8"}
						}
						rows = append(rows, row)
					}
					value = rows
				case fabricMulticastPath:
					rows := []fabricMulticastReplication{}
					if mode == "match" {
						rows = append(rows, fabricMulticastReplication{"fabric", "NATIVE_MULTICAST"})
					}
					if mode == "update" {
						rows = append(rows, fabricMulticastReplication{"fabric", "HEADEND_REPLICATION"})
					}
					value = rows
				default:
					t.Error("unexpected API", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": value})
			})
			plan, err := c.compileFabricConfiguration(raw)
			invalid := mode == "invalid-vn"
			if (err != nil) != invalid {
				t.Fatal("compile", err)
			}
			if invalid {
				if plan != nil || calls != 0 {
					t.Fatal("invalid model performed work")
				}
				return
			}
			ctx := context.Background()
			if mode != "legacy" {
				write, err := plan.next(ctx)
				if !errors.Is(err, errConfigurationDeviceAdmission) || write != nil || calls != 0 {
					t.Fatal("multicast Apply bypassed gate or planned prerequisites", err)
				}
			}
			drift, err := plan.report(ctx)
			if err != nil {
				t.Fatal("report", err)
			}
			count := 2
			if mode == "match" || mode == "legacy" {
				count = 0
			}
			if len(drift) != count || writes != 0 {
				t.Fatal("incorrect drift", len(drift), writes)
			}
			for _, entry := range drift {
				expected := "Add"
				if mode == "update" {
					expected = "Update"
				}
				if string(entry.Change) != expected {
					t.Fatal("wrong change", entry.Change)
				}
			}
			if mode == "legacy" {
				write, err := plan.next(ctx)
				if err != nil || write != nil {
					t.Fatal("legacy behavior changed", err)
				}
			}
		})
	}
}
