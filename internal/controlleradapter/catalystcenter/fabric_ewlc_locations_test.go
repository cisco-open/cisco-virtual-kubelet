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

func TestFabricEWLCLocations(t *testing.T) {
	for _, mode := range []string{"omitted", "matching", "empty", "addition", "removal", "anchor", "missing-site", "changed-id", "duplicate-site", "unknown-observation", "null-observation"} {
		t.Run(mode, func(t *testing.T) {
			sites := sampleSites()
			primary := map[string]string{"Global/Lab/Building": "building"}
			secondary := map[string]string{}
			var want *wirelessManagedAPIntent
			switch mode {
			case "matching":
				want = &wirelessManagedAPIntent{primary: []string{"Global/Lab/Building"}}
			case "empty":
				primary = map[string]string{}
				want = &wirelessManagedAPIntent{primary: []string{}}
			case "addition":
				want = &wirelessManagedAPIntent{secondary: []string{"Global/Lab"}}
			case "removal":
				want = &wirelessManagedAPIntent{primary: []string{}}
			case "anchor":
				want = &wirelessManagedAPIntent{anchor: []string{}}
			case "missing-site":
				sites = sites[:1]
			case "changed-id":
				primary["Global/Lab/Building"] = "replacement"
			case "duplicate-site":
				sites = append(sites, sites[0])
			case "unknown-observation":
				primary["Global/Unknown"] = "unknown"
			case "null-observation":
				secondary = nil
			}
			got, err := resolveFabricEWLCLocations("switch", want, sites, primary, secondary)
			valid := mode == "omitted" || mode == "matching" || mode == "empty"
			if (err == nil) != valid {
				t.Fatal(got, err)
			}
			if !valid {
				if got != nil {
					t.Fatal("partial evidence")
				}
				return
			}
			if len(got.digest) != 64 {
				t.Fatal("missing digest")
			}
			primary["Global/Other"] = "other"
			if got.primary["Global/Other"] != "" {
				t.Fatal("aliased native evidence")
			}
		})
	}
}

func TestFabricEWLCLocationsNative(t *testing.T) {
	for _, mode := range []string{"matching", "missing-assignment", "unsupported", "changed-count"} {
		t.Run(mode, func(t *testing.T) {
			counts := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("placement preflight mutated device")
					w.WriteHeader(400)
					return
				}
				reply := func(value any) { json.NewEncoder(w).Encode(map[string]any{"response": value}) }
				if r.URL.Path == sitesPath {
					reply(sampleSites())
					return
				}
				base := "/dna/intent/api/v1/wirelessControllers/switch/"
				if !strings.HasPrefix(r.URL.Path, base) {
					t.Error("unexpected target", r.URL.Path)
					w.WriteHeader(400)
					return
				}
				if mode == "unsupported" {
					w.WriteHeader(404)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/count") {
					counts++
					n := 1
					if mode == "missing-assignment" {
						n = 0
					}
					if mode == "changed-count" && counts == 2 {
						n = 2
					}
					reply(map[string]int{"primaryManagedApLocationsCount": n, "secondaryManagedApLocationsCount": 0, "anchorManagedApLocationsCount": 0})
					return
				}
				rows := []wirelessManagedAPLocation{}
				if strings.HasSuffix(r.URL.Path, "/primaryManagedApLocations") && mode != "missing-assignment" {
					rows = append(rows, wirelessManagedAPLocation{SiteID: "building", SitePath: "Global/Lab/Building"})
				}
				reply([]any{map[string]any{"managedApLocations": rows}})
			})
			got, err := c.fabricEWLCLocations(context.Background(), "switch", &wirelessManagedAPIntent{primary: []string{"Global/Lab/Building"}})
			if mode == "matching" {
				if err != nil || got.primary["Global/Lab/Building"] != "building" || counts != 4 {
					t.Fatal(got, err, counts)
				}
			} else if err == nil || got != nil {
				t.Fatal("accepted unverified placement", got, err)
			}
		})
	}
}
