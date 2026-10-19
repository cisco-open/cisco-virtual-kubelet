// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestAccessPointConfigurationCohortDispatch(t *testing.T) {
	for _, mode := range []string{"valid", "hostname-only", "missing-ap", "missing-floor", "initial-unassigned", "planning-race", "reversed", "frozen", "heterogeneous", "duplicate", "missing-guard", "denied", "replaced-ap", "changed-mac", "replaced-floor", "changed-rf", "assignment-lost", "late-target", "late-rf", "late-assignment", "guard-copy", "missing-task", "lost-response", "unauthorized", "async"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			profiles, err := parseAccessPointProfiles([]any{map[string]any{"name": "standard", "led_status": false, "radios": []any{map[string]any{"band": "5", "role": "SERVING", "power_assignment_mode": "CUSTOM", "power_level": 3}}}})
			if err != nil {
				t.Fatal(err)
			}
			wants := []accessPointDeviceIntent{}
			for i := 1; i <= 2; i++ {
				block := map[string]any{"configuration": "standard", "manage_hostname": true}
				if mode == "hostname-only" {
					delete(block, "configuration")
				}
				want, err := parseAccessPointDevice(map[string]any{"name": fmt.Sprintf("AP%d", i), "serial_number": fmt.Sprintf("SERIAL%d", i), "type": "AccessPoint", "site": "Global/Lab/Building/Floor", "state": "PROVISION", "rf_profile": "TYPICAL", "access_point": block}, profiles)
				if err != nil {
					t.Fatal(err)
				}
				wants = append(wants, want)
			}
			if mode == "heterogeneous" {
				other, err := parseAccessPointProfiles([]any{map[string]any{"name": "different", "led_status": true}})
				if err != nil {
					t.Fatal(err)
				}
				wants[1].profile = &other[0]
			}
			if mode == "duplicate" {
				wants[1] = wants[0]
			}
			if mode == "reversed" {
				wants[0], wants[1] = wants[1], wants[0]
			}
			changed, writes, guards, inventories := false, 0, 0, 0
			reads := map[string]int{}
			var submitted map[string]any
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path == accessPointConfigurePath {
					writes++
					if r.Method != "POST" || r.Header.Get("X-Auth-Token") != "test" {
						t.Error("wrong configure request")
					}
					if json.NewDecoder(r.Body).Decode(&submitted) != nil {
						t.Error("invalid request body")
					}
					switch mode {
					case "missing-task":
						fmt.Fprint(w, `{"response":{}}`)
					case "lost-response":
						connection, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						connection.Close()
					case "unauthorized":
						w.WriteHeader(401)
						fmt.Fprint(w, "private-response-detail")
					case "async":
						w.WriteHeader(202)
						fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					default:
						fmt.Fprint(w, `{"response":{"taskId":"task","url":"https://untrusted.invalid"}}`)
					}
					return
				}
				reads[r.URL.Path]++
				if r.Method != "GET" {
					t.Error("prerequisite observation mutated controller")
				}
				rows := []any{}
				switch r.URL.Path {
				case devicesPath:
					inventories++
					if mode == "planning-race" && inventories >= 2 {
						changed = true
					}
					for i := 1; i <= 2; i++ {
						if mode == "missing-ap" && i == 2 {
							continue
						}
						d := Device{ID: fmt.Sprintf("ap%d", i), Serial: fmt.Sprintf("SERIAL%d", i), Hostname: fmt.Sprintf("old%d", i), PlatformID: "C9130AXI", APEthernetMAC: fmt.Sprintf("02:00:00:00:00:0%d", i)}
						if changed && i == 2 {
							if mode == "replaced-ap" || mode == "late-target" || mode == "planning-race" {
								d.ID = "replacement"
							}
							if mode == "changed-mac" {
								d.APEthernetMAC = "02:00:00:00:00:03"
							}
						}
						rows = append(rows, d)
					}
				case sitesPath:
					id := "floor"
					if changed && mode == "replaced-floor" {
						id = "replacement"
					}
					sites := sampleSites()
					if mode != "missing-floor" {
						sites = append(sites, Site{ID: id, Name: "Floor", NameHierarchy: "Global/Lab/Building/Floor", ParentID: "building", Type: "floor"})
					}
					for _, s := range sites {
						rows = append(rows, s)
					}
				case wirelessRFPath:
					rf := map[string]any{"id": "rf", "rfProfileName": "TYPICAL", "defaultRfProfile": true, "enableRadioTypeA": true, "enableRadioTypeB": true, "enableRadioType6GHz": false}
					if changed && (mode == "changed-rf" || mode == "late-rf") {
						rf["enableRadioTypeB"] = false
					}
					rows = append(rows, rf)
				case assignedDevicesPath:
					for i := 1; i <= 2; i++ {
						if i == 2 && (mode == "initial-unassigned" || changed && (mode == "assignment-lost" || mode == "late-assignment")) {
							continue
						}
						rows = append(rows, map[string]any{"deviceId": fmt.Sprintf("ap%d", i), "siteId": r.URL.Query().Get("siteId")})
					}
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			guard := func(_ context.Context, digest string, members []accessPointConfigurationMember) error {
				guards++
				if len(digest) != 64 || len(members) != 2 || members[0].DeviceID != "ap1" || members[1].DeviceID != "ap2" || members[0].EthernetMAC != "02:00:00:00:00:01" || members[1].SiteID != "floor" || members[1].RFID != "rf" {
					t.Error("guard did not bind exact ordered cohort")
				}
				if mode == "denied" {
					return errConfigurationDeviceAdmission
				}
				if strings.HasPrefix(mode, "late-") {
					changed = true
				}
				if mode == "guard-copy" {
					members[0].EthernetMAC = "02:00:00:00:00:ff"
				}
				return nil
			}
			if mode == "missing-guard" {
				guard = nil
			}
			plan, err := c.planAccessPointConfiguration(ctx, wants, guard)
			if mode == "heterogeneous" || mode == "duplicate" || mode == "missing-guard" || mode == "missing-ap" || mode == "missing-floor" || mode == "initial-unassigned" || mode == "planning-race" {
				if err == nil || plan != nil || writes != 0 {
					t.Fatal("invalid cohort accepted")
				}
				return
			}
			if err != nil || plan == nil || len(plan.digest) != 64 {
				t.Fatal("missing cohort plan", err)
			}
			if mode == "frozen" {
				plan.members[0].EthernetMAC = "02:00:00:00:00:ff"
				wants[0].profile.payload["ledStatus"] = true
				wants[0].profile.payload["radioConfigurations"].([]any)[0].(map[string]any)["powerLevel"] = 8
				wants[1].inventory.reference.Serial = "OTHER"
			}
			if mode == "replaced-ap" || mode == "changed-mac" || mode == "replaced-floor" || mode == "changed-rf" || mode == "assignment-lost" {
				changed = true
			}
			task, err := plan.submit(ctx)
			valid := mode == "valid" || mode == "hostname-only" || mode == "reversed" || mode == "frozen" || mode == "guard-copy"
			if (err == nil) != valid || valid && task.ID != "task" {
				t.Fatal("unexpected dispatch result", err)
			}
			if err != nil && strings.Contains(err.Error(), "private-response-detail") {
				t.Fatal("private response leaked")
			}
			expectedWrites := 0
			if valid || mode == "missing-task" || mode == "lost-response" || mode == "unauthorized" || mode == "async" {
				expectedWrites = 1
			}
			if writes != expectedWrites {
				t.Fatal("unsafe native write count", writes)
			}
			if _, err := plan.submit(ctx); err == nil || writes != expectedWrites {
				t.Fatal("one-use plan replayed")
			}
			if expectedWrites == 1 {
				if mode != "hostname-only" {
					expected, _ := parseAccessPointProfiles([]any{map[string]any{"name": "standard", "led_status": false, "radios": []any{map[string]any{"band": "5", "role": "SERVING", "power_assignment_mode": "CUSTOM", "power_level": 3}}}})
					raw, _ := json.Marshal(expected[0].payload)
					var settings map[string]any
					json.Unmarshal(raw, &settings)
					actual := map[string]any{}
					for key, value := range submitted {
						if key != "apList" {
							actual[key] = value
						}
					}
					if !reflect.DeepEqual(settings, actual) {
						t.Fatal("cohort request changed selected settings or configure gates")
					}
				}
				expected := []any{map[string]any{"macAddress": "02:00:00:00:00:01", "apNameNew": "AP1"}, map[string]any{"macAddress": "02:00:00:00:00:02", "apNameNew": "AP2"}}
				if !reflect.DeepEqual(submitted["apList"], expected) || submitted["ledStatus"] == true {
					t.Fatal("batch changed targets or frozen settings")
				}
				if mode == "hostname-only" {
					if len(submitted) != 2 || len(submitted["radioConfigurations"].([]any)) != 0 {
						t.Fatal("hostname-only request invented settings")
					}
				} else if submitted["radioConfigurations"].([]any)[0].(map[string]any)["powerLevel"] != float64(3) {
					t.Fatal("nested radio payload was not frozen")
				}
			}
			if valid {
				for _, path := range []string{devicesPath, sitesPath, wirelessRFPath, assignedDevicesPath} {
					if reads[path] != 4 {
						t.Fatal("cohort catalog must be shared within each of four fresh observations", path, reads[path])
					}
				}
			}
			if guards > 1 {
				t.Fatal("repeated mutation authority request")
			}
		})
	}
}
