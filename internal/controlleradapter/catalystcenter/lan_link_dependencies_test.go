// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestLANLinkCompleteDependencyFootprint(t *testing.T) {
	for _, mode := range []string{"unchanged", "pool-race", "origin-race", "topology-during-pool", "delete-without-pool"} {
		t.Run(mode, func(t *testing.T) {
			_, _, f, api := newManagedFabricDevicesBulkFixture(t, "valid")
			poolReads, originReads := 0, 0
			topologyChanged := false
			ports := []lanLinkInterface{lanPortFixture("edge-0", "TenGigabitEthernet1/0/1"), lanPortFixture("edge-1", "TenGigabitEthernet1/0/2")}
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
				var response any
				switch r.URL.Path {
				case devicesPath:
					response = f.inventory
				case "/dna/intent/api/v1/interface/network-device/edge-0":
					response = []lanLinkInterface{ports[0]}
				case "/dna/intent/api/v1/interface/network-device/edge-1":
					response = []lanLinkInterface{ports[1]}
				case lanPhysicalTopologyPath:
					nodes := []lanTopologyNode{}
					for _, device := range f.inventory {
						nodes = append(nodes, lanTopologyNode{ID: device.ID, IP: device.ManagementIP, Type: "device"})
					}
					link := lanPhysicalLink{ID: "physical-link", Source: "edge-0", Target: "edge-1", StartID: ports[0].ID, EndID: ports[1].ID, StartName: ports[0].PortName, EndName: ports[1].PortName, Status: "up"}
					if topologyChanged {
						link.ID = "new-link"
					}
					response = lanPhysicalTopology{Nodes: nodes, Links: []lanPhysicalLink{link}}
				case lanSessionStatusPath:
					originReads++
					devices := []lanDiscoveredDevice{}
					for _, device := range f.inventory[:2] {
						devices = append(devices, lanDiscoveredDevice{Name: device.Hostname, Serial: device.Serial, State: "recorded", Addresses: []string{device.ManagementIP}})
					}
					session := lanSession{ID: "origin", Site: "Global/Lab", PrimaryIP: "198.51.100.200", Ports: []string{}, Pools: []lanSessionPool{}, Action: "STOP", Status: "recorded", Created: "2026-10-15T00:00:00Z", Discovered: devices}
					if mode == "origin-race" && originReads > 1 {
						session.ID = "replacement-origin"
					}
					response = []lanSession{session}
				case sitesPath:
					response = sampleSites()
				case globalPoolsPath:
					response = fixturePools()
				case sitePoolsPath:
					poolReads++
					rows := fixtureReservations()
					if mode == "pool-race" && poolReads > 1 {
						rows[0].ID = "replacement-pool"
					}
					if mode == "topology-during-pool" {
						topologyChanged = true
					}
					response = rows
				default:
					t.Error("unexpected read", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": response})
			})
			wants, err := parseLANLinks(lanLinkModel(t, lanLinkFixture()))
			if err != nil {
				t.Fatal(err)
			}
			want := wants[0]
			want.primaryIP = f.inventory[0].ManagementIP
			want.secondaryIP = f.inventory[1].ManagementIP
			want.pool = "access"
			if mode == "delete-without-pool" {
				want.action = "DELETE_LINK"
				want.pool = ""
			}
			deps, targets, err := c.lanLinkDependencyTargets(context.Background(), api, "lab", want)
			valid := mode == "unchanged" || mode == "delete-without-pool"
			if (err == nil) != valid {
				t.Fatal("unexpected combined dependency validity", err)
			}
			if valid && (len(targets) != 2 || deps.adjacency.link == nil || deps.observationDigest() == "") {
				t.Fatal("dependency footprint incomplete")
			}
			if mode == "unchanged" && (deps.pool == nil || deps.pool.pool.primary.ID != "reservation") {
				t.Fatal("pool not bound to managed endpoint footprint")
			}
			if mode == "delete-without-pool" && (deps.pool != nil || poolReads != 0 || originReads != 0) {
				t.Fatal("delete manufactured a pool/discovery dependency")
			}
		})
	}
}
