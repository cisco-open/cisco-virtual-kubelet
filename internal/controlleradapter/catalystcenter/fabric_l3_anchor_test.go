// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestFabricL3AnchorReportAndAdmission(t *testing.T) {
	for _, mode := range []string{"matching", "different", "unanchored", "missing", "missing-fabric", "missing-site"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				calls++
				if r.Method != "GET" {
					t.Errorf("unexpected mutation %s", r.Method)
					w.WriteHeader(500)
					return
				}
				switch r.URL.Path {
				case fabricL3NetworksPath:
					rows := []fabricL3Network{{ID: "vn", Name: "CVK-Lab", Anchor: "fabric", Fabrics: []string{"fabric", "other"}}}
					switch mode {
					case "different":
						rows[0].Anchor = "other"
					case "unanchored":
						rows[0].Anchor = ""
					case "missing":
						rows = nil
					}
					if rows == nil {
						rows = []fabricL3Network{}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case sitesPath:
					rows := sampleSites()
					if mode == "missing-site" {
						rows = rows[:1]
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case fabricSitesPath:
					yes := true
					rows := []fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &yes}}
					if mode == "missing-fabric" {
						rows = []fabricSite{}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			raw := []byte(strings.Replace(fabricL3Model, `"name":"CVK-Lab"`, `"name":"CVK-Lab","anchor_site":"Global/Lab"`, 1))
			plan, err := c.compileFabricConfiguration(raw)
			if err != nil {
				t.Fatal(err)
			}
			if write, err := plan.next(context.Background()); write != nil || !errors.Is(err, errConfigurationDeviceAdmission) || calls != 0 {
				t.Fatal("anchor Apply escaped composition gate", write, err, calls)
			}
			drift, err := plan.report(context.Background())
			if mode == "missing-fabric" || mode == "missing-site" {
				if !errors.Is(err, errConfigurationBusy) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "matching" {
				if len(drift) != 0 {
					t.Fatal(drift)
				}
				return
			}
			want := "Update"
			if mode == "missing" {
				want = "Add"
			}
			if len(drift) != 1 || string(drift[0].Change) != want {
				t.Fatal(drift)
			}
		})
	}
}

func TestFabricL3AnchorModelValidation(t *testing.T) {
	for _, field := range []string{`"anchor_site":""`, `"anchor_site":"fabric-id"`, `"anchor_site":"Global"`, `"anchor_site":42`, `"anchoredSiteId":"uuid"`, `"extra":true`} {
		raw := strings.Replace(fabricL3Model, `"name":"CVK-Lab"`, `"name":"CVK-Lab",`+field, 1)
		if _, err := (&client{}).compileFabricConfiguration([]byte(raw)); err == nil {
			t.Fatal("accepted", field)
		}
	}
	// Null is omission, not an instruction to unanchor a live network.
	raw := strings.Replace(fabricL3Model, `"name":"CVK-Lab"`, `"name":"CVK-Lab","anchor_site":null`, 1)
	plan, err := (&client{}).compileFabricConfiguration([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, gated := plan.configurationExecutor.(*fabricAnchorAdmissionGate); gated {
		t.Fatal("null anchor enabled anchoring")
	}
	for _, name := range []string{"INFRA_VN", "DEFAULT_VN"} {
		raw := fmt.Sprintf(`{"catalyst_center":{"fabric":{"l3_virtual_networks":[{"name":%q,"anchor_site":"Global/Lab"}]}}}`, name)
		if _, err := (&client{}).compileFabricConfiguration([]byte(raw)); err == nil {
			t.Fatal("system VN anchor accepted")
		}
	}
}
