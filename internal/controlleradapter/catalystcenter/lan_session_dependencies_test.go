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

func TestLANSessionDependencies(t *testing.T) {
	wants, _, err := parseLANSessionIntents([]byte(lanSessionModelFixture()), false)
	if err != nil {
		t.Fatal(err)
	}
	want := wants[0]
	want.Pools = []lanSessionPoolIntent{{Name: "access", Role: "PRINCIPAL_IP_ADDRESS_POOL"}}
	// A projected value must not enter the returned dependency evidence.
	want.Password = "fixture-secret"
	for _, mode := range []string{"unchanged", "reordered", "site-replaced", "port-replaced", "seed-replaced", "pool-replaced", "pool-parent-changed", "pool-owner-changed", "missing-pool", "non-lan-pool", "missing-discovery-site"} {
		t.Run(mode, func(t *testing.T) {
			changed := false
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
				case sitesPath:
					rows := sampleSites()
					if changed && mode == "site-replaced" {
						rows[2].ID = "new-building"
					}
					if changed && mode == "missing-discovery-site" {
						rows = rows[:2]
					}
					if changed && mode == "reordered" {
						rows[0], rows[2] = rows[2], rows[0]
					}
					response = rows
				case devicesPath:
					rows := []Device{{ID: "seed", ManagementIP: "192.0.2.10", Serial: "SEED-SERIAL"}, {ID: "peer", ManagementIP: "192.0.2.11", Serial: "PEER-SERIAL"}}
					if changed && mode == "seed-replaced" {
						rows[0].Serial = "NEW-SEED"
					}
					if changed && mode == "reordered" {
						rows[0], rows[1] = rows[1], rows[0]
					}
					response = rows
				case "/dna/intent/api/v1/interface/network-device/seed":
					rows := []lanLinkInterface{lanPortFixture("seed", "TenGigabitEthernet1/0/1")}
					if changed && mode == "port-replaced" {
						rows[0].ID = "new-port"
					}
					response = rows
				case globalPoolsPath:
					rows := fixturePools()
					if changed && mode == "pool-parent-changed" {
						rows[0].Name = "renamed-parent"
					}
					response = rows
				case sitePoolsPath:
					rows := fixtureReservations()
					if changed {
						switch mode {
						case "pool-replaced":
							rows[0].ID = "new-reservation"
						case "pool-owner-changed":
							rows[0].SiteID = "building"
						case "missing-pool":
							rows = []ipReservation{}
						case "non-lan-pool":
							rows[0].Type = "Generic"
						}
					}
					response = rows
				default:
					t.Error("unexpected API", r.URL)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": response})
			})
			before, err := c.lanSessionDependencies(context.Background(), want)
			if err != nil {
				t.Fatal(err)
			}
			if before.site.ID != "area" || len(before.discoverySites) != 1 || before.discoverySites[0].ID != "building" || len(before.seeds) != 2 || len(before.primaryPorts) != 1 || len(before.pools) != 1 || before.pools[0].evidence.primary.ID != "reservation" || before.pools[0].evidence.digest == "" {
				t.Fatal("incomplete dependency binding")
			}
			if strings.Contains(fmt.Sprintf("%+v %#v", before, before), want.Password) {
				t.Fatal("credential entered dependency evidence")
			}
			changed = true
			err = c.revalidateLANSessionDependencies(context.Background(), want, before)
			if (err == nil) != (mode == "unchanged" || mode == "reordered") {
				t.Fatal("wrong dependency revalidation result", err)
			}
		})
	}
}
