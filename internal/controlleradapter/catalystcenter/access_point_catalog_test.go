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

func TestAccessPointCohortAssignmentCatalogScope(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("second-floor-missing-%t", missing), func(t *testing.T) {
			profiles, err := parseAccessPointProfiles([]any{map[string]any{"name": "standard", "led_status": false}})
			if err != nil {
				t.Fatal(err)
			}
			wants := []accessPointDeviceIntent{}
			for i := 1; i <= 2; i++ {
				want, err := parseAccessPointDevice(map[string]any{"name": fmt.Sprintf("AP%d", i), "serial_number": fmt.Sprintf("SERIAL%d", i), "type": "AccessPoint", "site": fmt.Sprintf("Global/Lab/Building/Floor%d", i), "state": "PROVISION", "rf_profile": "TYPICAL", "access_point": map[string]any{"configuration": "standard"}}, profiles)
				if err != nil {
					t.Fatal(err)
				}
				wants = append(wants, want)
			}
			reads, scopes := map[string]int{}, map[string]int{}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("planning wrote to the controller")
				}
				reads[r.URL.Path]++
				rows := []any{}
				switch r.URL.Path {
				case devicesPath:
					for i := 1; i <= 2; i++ {
						rows = append(rows, Device{ID: fmt.Sprintf("ap%d", i), Serial: fmt.Sprintf("SERIAL%d", i), Hostname: fmt.Sprintf("AP%d", i), PlatformID: "C9130AXI", APEthernetMAC: fmt.Sprintf("02:00:00:00:00:0%d", i)})
					}
				case sitesPath:
					for _, site := range sampleSites() {
						rows = append(rows, site)
					}
					for i := 1; i <= 2; i++ {
						rows = append(rows, Site{ID: fmt.Sprintf("floor%d", i), Name: fmt.Sprintf("Floor%d", i), NameHierarchy: fmt.Sprintf("Global/Lab/Building/Floor%d", i), ParentID: "building", Type: "floor"})
					}
				case wirelessRFPath:
					rows = append(rows, map[string]any{"id": "rf", "rfProfileName": "TYPICAL", "defaultRfProfile": true, "enableRadioTypeA": true, "enableRadioTypeB": true, "enableRadioType6GHz": false})
				case assignedDevicesPath:
					site := r.URL.Query().Get("siteId")
					scopes[site]++
					if site != "floor1" && site != "floor2" {
						t.Error("unbounded assignment query")
					}
					if !missing || site != "floor2" {
						rows = append(rows, map[string]string{"deviceId": "ap" + site[len("floor"):], "siteId": site})
					}
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			plan, err := c.planAccessPointConfiguration(context.Background(), wants, func(context.Context, string, []accessPointConfigurationMember) error {
				t.Fatal("preparation requested dispatch authority")
				return nil
			})
			if missing {
				if err == nil || plan != nil {
					t.Fatal("first floor's assignment authorized the second AP")
				}
				return
			}
			if err != nil || plan == nil || len(plan.members) != 2 || plan.members[1].SiteID != "floor2" {
				t.Fatal("cohort lost its per-member floor binding", err)
			}
			for _, path := range []string{devicesPath, sitesPath, wirelessRFPath} {
				if reads[path] != 2 {
					t.Fatal("catalog was not scoped to each fresh observation", path, reads[path])
				}
			}
			if scopes["floor1"] != 2 || scopes["floor2"] != 2 {
				t.Fatal("assignments were reused across floors or observations", scopes)
			}
		})
	}
}
