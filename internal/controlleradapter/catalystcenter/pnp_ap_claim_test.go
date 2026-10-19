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

func TestPnPAccessPointClaim(t *testing.T) {
	for _, mode := range []string{"floor", "building", "area", "missing-floor", "missing-rf", "late-floor", "late-rf"} {
		t.Run(mode, func(t *testing.T) {
			want := pnpDeviceIntent{Name: "ap", State: "PNP", Serial: "FOC12345678", Site: "Global/Lab/Building/Floor", PID: "C9130AXI-E", Type: "AccessPoint", RFProfile: "LabRF"}
			if mode == "building" {
				want.Site = "Global/Lab/Building"
			}
			if mode == "area" {
				want.Site = "Global/Lab"
			}
			posts, rfReads := 0, 0
			late := false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				switch r.URL.Path {
				case pnpDevicesPath:
					row := pnpFixture("ap-id", "FOC12345678")
					info := row["deviceInfo"].(map[string]any)
					info["hostname"], info["pid"] = want.Name, want.PID
					json.NewEncoder(w).Encode([]any{row})
				case devicesPath:
					fmt.Fprint(w, `{"response":[]}`)
				case sitesPath:
					sites := sampleSites()
					floor := Site{ID: "floor-id", Name: "Floor", NameHierarchy: "Global/Lab/Building/Floor", ParentID: "building", Type: "floor"}
					if late && mode == "late-floor" {
						floor.ID = "replacement-floor"
					}
					if mode != "missing-floor" {
						sites = append(sites, floor)
					}
					json.NewEncoder(w).Encode(map[string]any{"response": sites})
				case wirelessRFPath:
					rfReads++
					id := "rf-id"
					if late && mode == "late-rf" {
						id = "replacement-rf"
					}
					rows := []any{map[string]any{"id": id, "rfProfileName": "LabRF", "defaultRfProfile": false, "enableRadioTypeA": true, "enableRadioTypeB": true, "enableRadioType6GHz": false}}
					if mode == "missing-rf" {
						rows = []any{}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case pnpSiteClaimPath:
					posts++
					var body map[string]any
					if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Error("claim request")
					}
					if body["type"] != "AccessPoint" || body["rfProfile"] != "LabRF" || body["deviceId"] != "ap-id" || body["siteId"] != "floor-id" {
						t.Error("lost AP claim binding")
					}
					fmt.Fprint(w, `{"response":"accepted"}`)
				default:
					t.Error("unexpected API", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			plan, err := c.planPnPSiteClaim(context.Background(), want, "ap-id", nil)
			valid := mode == "floor" || strings.HasPrefix(mode, "late-")
			if (err == nil) != valid {
				t.Fatal("AP preflight", err)
			}
			if !valid {
				if posts != 0 {
					t.Fatal("invalid AP submitted")
				}
				if (mode == "area" || mode == "building") && rfReads != 0 {
					t.Fatal("invalid site passed dependency gate")
				}
				return
			}
			late = true
			got, err := plan.submit(context.Background())
			if (err == nil) != (mode == "floor") {
				t.Fatal("AP dispatch", err)
			}
			if mode == "floor" {
				if posts != 1 || got == nil || got.siteID != "floor-id" {
					t.Fatal("missing AP acknowledgement")
				}
			} else if posts != 0 {
				t.Fatal("changed AP dependency submitted")
			}
			if _, err := plan.submit(context.Background()); err == nil {
				t.Fatal("AP replay allowed")
			}
		})
	}
}
