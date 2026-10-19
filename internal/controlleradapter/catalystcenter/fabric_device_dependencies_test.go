// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestFabricDeviceDependencies(t *testing.T) {
	for _, mode := range []string{"site", "zone", "unprovisioned", "changed-provision", "wrong-role", "unassigned", "missing-site", "missing-fabric", "missing-zone", "missing-parent-member", "parent-role", "duplicate-serial", "peer-serial", "changed-parent", "changed-peer"} {
		t.Run(mode, func(t *testing.T) {
			raw := fabricEdgeIntentModel
			if mode == "wrong-role" {
				raw = strings.Replace(raw, `"name":"edge"`, `"name":"edge","device_role":"ACCESS","role_source":"MANUAL"`, 1)
			}
			zone := mode == "zone" || mode == "missing-zone" || mode == "missing-parent-member" || mode == "parent-role"
			if zone {
				raw = strings.Replace(raw, `"name":"edge"`, `"name":"edge","fabric_zone":"Global/Lab/Building"`, 1)
			}
			wants, err := parseFabricDeviceIntents([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			revision := false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("mutation in dependencies")
					w.WriteHeader(500)
					return
				}
				var rows any
				switch r.URL.Path {
				case devicesPath:
					devices := []Device{{ID: "edge-id", Hostname: "edge", Serial: "FOC12345678"}, {ID: "peer-id", Hostname: "peer", Serial: "FOC87654321"}}
					if mode == "duplicate-serial" {
						devices[1].Serial = devices[0].Serial
					}
					if mode == "peer-serial" {
						devices[1].Serial = ""
					}
					if revision && mode == "changed-peer" {
						devices[1].Serial = "FOC99999999"
					}
					rows = devices
				case sitesPath:
					sites := sampleSites()
					if mode == "missing-site" {
						sites = sites[:2]
					}
					rows = sites
				case fabricSitesPath:
					no := false
					fabrics := []fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &no}}
					if mode == "missing-fabric" {
						fabrics = []fabricSite{}
					}
					if revision && mode == "changed-parent" {
						fabrics[0].ID = "replacement"
					}
					rows = fabrics
				case fabricZonesPath:
					zones := []fabricZone{{ID: "zone", SiteID: "building", Authentication: "Closed Authentication"}}
					if mode == "missing-zone" {
						zones = []fabricZone{}
					}
					rows = zones
				case wiredProvisionPath:
					provisioned := []wiredProvisionedDevice{{ID: "provisioned", DeviceID: "edge-id", SiteID: "building"}}
					if mode == "unprovisioned" {
						provisioned = []wiredProvisionedDevice{}
					}
					if revision && mode == "changed-provision" {
						provisioned[0].ID = "replacement"
					}
					rows = provisioned
				case assignedDevicesPath:
					assignments := []map[string]string{{"deviceId": "edge-id", "siteId": "building"}}
					if mode == "unassigned" {
						assignments = []map[string]string{}
					}
					rows = assignments
				case fabricDevicesPath:
					fid := r.URL.Query().Get("fabricId")
					members := []fabricDeviceConfiguration{{ID: "peer-row", NetworkDeviceID: "peer-id", FabricID: fid, Roles: []string{"EDGE_NODE"}}}
					if zone && fid == "fabric" && mode != "missing-parent-member" {
						roles := []string{"EDGE_NODE"}
						if mode == "parent-role" {
							roles = []string{"CONTROL_PLANE_NODE"}
						}
						members = append(members, fabricDeviceConfiguration{ID: "parent-edge", NetworkDeviceID: "edge-id", FabricID: fid, Roles: roles})
					}
					rows = members
				default:
					t.Errorf("unexpected %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			deps, err := c.fabricDeviceDependencies(context.Background(), wants[0])
			valid := mode == "site" || mode == "zone" || mode == "changed-parent" || mode == "changed-peer" || mode == "changed-provision"
			if !valid {
				if err == nil || deps != nil {
					t.Fatal("invalid dependency accepted", deps, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantFabric := "fabric"
			if zone {
				wantFabric = "zone"
			}
			if deps.native.FabricID != wantFabric || deps.target.Serial != "foc12345678" || len(deps.physical) != 2 || len(deps.digest) != 64 {
				t.Fatal(deps)
			}
			revision = true
			next, err := c.fabricDeviceDependencies(context.Background(), wants[0])
			if err != nil {
				t.Fatal(err)
			}
			changed := mode == "changed-parent" || mode == "changed-peer" || mode == "changed-provision"
			if (next.digest != deps.digest) != changed {
				t.Fatal("incorrect dependency digest stability")
			}
		})
	}
}
