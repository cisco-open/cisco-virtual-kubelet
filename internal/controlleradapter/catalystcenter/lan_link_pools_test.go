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

func TestLANLinkDiscoveryPoolBinding(t *testing.T) {
	for _, mode := range []string{"unchanged", "reordered", "missing-origin", "serial-alias", "address-changed", "ambiguous-origin", "site-replaced", "pool-replaced", "pool-parent-changed", "pool-owner-changed", "missing-pool", "not-lan", "unknown-session-state", "seed-only"} {
		t.Run(mode, func(t *testing.T) {
			changed := false
			endpoints, _ := lanTopologyFixture()
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
				case lanSessionStatusPath:
					sessions := []lanSession{{ID: "origin", Site: "Global/Lab", PrimaryIP: "198.51.100.200", Ports: []string{}, Pools: []lanSessionPool{}, Action: "STOP", Status: "recorded", Created: "2026-10-15T00:00:00Z", Discovered: []lanDiscoveredDevice{{Name: "seed", Serial: "seed-serial", State: "recorded", Addresses: []string{"192.0.2.10"}}, {Name: "peer", Serial: "peer-serial", State: "recorded", Addresses: []string{"192.0.2.11"}}}}}
					if changed {
						switch mode {
						case "missing-origin":
							sessions = []lanSession{}
						case "serial-alias":
							sessions[0].Discovered[0].Serial = "other-serial"
						case "address-changed":
							sessions[0].Discovered[0].Addresses = []string{"192.0.2.99"}
						case "ambiguous-origin":
							other := sessions[0]
							other.ID = "other-origin"
							other.Site = "Global/Lab/Building"
							sessions = append(sessions, other)
						case "unknown-session-state":
							sessions[0].Status = "NEW_STATE"
						case "seed-only":
							sessions[0].Discovered = nil
							sessions[0].PrimaryIP = "192.0.2.10"
							sessions[0].PeerIP = "192.0.2.11"
						case "reordered":
							sessions[0].Discovered[0], sessions[0].Discovered[1] = sessions[0].Discovered[1], sessions[0].Discovered[0]
						}
					}
					response = sessions
				case sitesPath:
					rows := sampleSites()
					if changed && mode == "site-replaced" {
						rows[1].ID = "new-area"
					}
					if changed && mode == "reordered" {
						rows[0], rows[2] = rows[2], rows[0]
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
						case "not-lan":
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
			wants, err := parseLANLinks(lanLinkModel(t, lanLinkFixture()))
			if err != nil {
				t.Fatal(err)
			}
			want := wants[0]
			want.pool = "access"
			before, err := c.lanLinkPoolDependencies(context.Background(), want, endpoints)
			if err != nil {
				t.Fatal(err)
			}
			if before.pool.primary.ID != "reservation" || before.primary.site.ID != "area" || before.secondary.site.ID != "area" || len(before.primary.origins) != 1 || len(before.secondary.origins) != 1 {
				t.Fatal("pool origin/owner/identity incomplete")
			}
			changed = true
			err = c.revalidateLANLinkPoolDependencies(context.Background(), want, endpoints, before)
			if (err == nil) != (mode == "unchanged" || mode == "reordered") {
				t.Fatal("unexpected origin/pool revalidation", err)
			}
		})
	}
}
