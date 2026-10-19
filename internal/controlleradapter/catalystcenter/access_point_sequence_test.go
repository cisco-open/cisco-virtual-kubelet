// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func accessPointSequenceIntents(t *testing.T) []accessPointDeviceIntent {
	t.Helper()
	profiles, err := parseAccessPointProfiles([]any{map[string]any{"name": "Alpha", "led_status": false}, map[string]any{"name": "Beta", "led_status": true}})
	if err != nil {
		t.Fatal(err)
	}
	wants := []accessPointDeviceIntent{}
	for i := 1; i <= 4; i++ {
		block := map[string]any{"manage_hostname": true}
		if i <= 2 {
			block["configuration"] = "Alpha"
		} else if i == 3 {
			block["configuration"] = "Beta"
		}
		want, err := parseAccessPointDevice(map[string]any{"name": fmt.Sprintf("AP%d", i), "serial_number": fmt.Sprintf("SERIAL%d", i), "type": "AccessPoint", "site": "Global/Lab/Building/Floor", "state": "PROVISION", "rf_profile": "TYPICAL", "access_point": block}, profiles)
		if err != nil {
			t.Fatal(err)
		}
		wants = append(wants, want)
	}
	return wants
}

func TestAccessPointConfigurationCohortGrouping(t *testing.T) {
	for _, mode := range []string{"valid", "reordered", "same-settings-different-names", "case-normalized-order", "duplicate-name", "duplicate-serial", "missing-serial", "conflicting-profile", "invalid-state", "reserved-name"} {
		t.Run(mode, func(t *testing.T) {
			wants := accessPointSequenceIntents(t)
			switch mode {
			case "reordered":
				for i, j := 0, len(wants)-1; i < j; i, j = i+1, j-1 {
					wants[i], wants[j] = wants[j], wants[i]
				}
			case "same-settings-different-names":
				wants[2].profile.payload["ledStatus"] = false
			case "case-normalized-order":
				wants[0].inventory.reference.Serial = "serial1"
			case "duplicate-name":
				wants[2].inventory.reference.Name = wants[0].inventory.reference.Name
			case "duplicate-serial":
				wants[2].inventory.reference.Serial = "serial1"
			case "missing-serial":
				wants[2].inventory.reference.Serial = ""
			case "conflicting-profile":
				wants[2].profile.name = "Alpha"
			case "invalid-state":
				wants[2].state = "ASSIGN"
			case "reserved-name":
				wants[2].profile.name = apHostnameOnlyCohort
			}
			groups, err := groupAccessPointConfigurations(wants)
			valid := mode == "valid" || mode == "reordered" || mode == "same-settings-different-names" || mode == "case-normalized-order"
			if !valid {
				if err == nil || groups != nil {
					t.Fatal("invalid group selection accepted")
				}
				return
			}
			if err != nil || len(groups) != 3 || groups[0].name != apHostnameOnlyCohort || groups[1].name != "Alpha" || groups[2].name != "Beta" || len(groups[1].members) != 2 || groups[1].members[0].inventory.reference.Name != "AP1" {
				t.Fatal("lost named cohort or stable member order", err)
			}
			if len(groups[0].members[0].profile.payload) != 0 {
				t.Fatal("rename-only group inherited named settings")
			}
			if mode != "same-settings-different-names" {
				for _, want := range wants {
					if want.profile != nil && want.profile.name == "Alpha" {
						want.profile.payload["ledStatus"] = true
					}
				}
				if groups[1].members[0].profile.payload["ledStatus"] != false {
					t.Fatal("grouping retained mutable input settings")
				}
			}
		})
	}
}

func TestAccessPointWholeSequenceDispatch(t *testing.T) {
	for _, mode := range []string{"valid", "concurrent", "selected-beta", "reordered", "later-invalid", "cross-group-mac-alias", "planning-race", "before-other-target", "late-other-target", "denied", "lost-receipt", "frozen", "guard-copy"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			wants := accessPointSequenceIntents(t)
			if mode == "later-invalid" {
				wants[2].rfProfile = "missing"
			}
			if mode == "reordered" {
				wants[0], wants[3] = wants[3], wants[0]
			}
			changed, writes, guards, inventories := false, 0, 0, 0
			var body map[string]any
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path == accessPointConfigurePath {
					writes++
					if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Error("wrong configuration request")
					}
					if mode == "lost-receipt" {
						fmt.Fprint(w, `{}`)
					} else {
						fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					}
					return
				}
				if r.Method != "GET" {
					t.Error("preflight mutated the controller")
				}
				rows := []any{}
				switch r.URL.Path {
				case devicesPath:
					inventories++
					if mode == "planning-race" && inventories >= 3 {
						changed = true
					}
					for i := 1; i <= 4; i++ {
						id, mac := fmt.Sprintf("ap%d", i), fmt.Sprintf("02:00:00:00:00:0%d", i)
						if i == 3 && changed {
							id = "replaced-third-ap"
						}
						if mode == "cross-group-mac-alias" && i == 3 {
							mac = "02:00:00:00:00:01"
						}
						rows = append(rows, Device{ID: id, Serial: fmt.Sprintf("SERIAL%d", i), Hostname: fmt.Sprintf("AP%d", i), PlatformID: "C9130AXI", APEthernetMAC: mac})
					}
				case sitesPath:
					for _, site := range append(sampleSites(), Site{ID: "floor", Name: "Floor", NameHierarchy: "Global/Lab/Building/Floor", ParentID: "building", Type: "floor"}) {
						rows = append(rows, site)
					}
				case wirelessRFPath:
					rows = append(rows, map[string]any{"id": "rf", "rfProfileName": "TYPICAL", "defaultRfProfile": true, "enableRadioTypeA": true, "enableRadioTypeB": true, "enableRadioType6GHz": false})
				case assignedDevicesPath:
					for i := 1; i <= 4; i++ {
						rows = append(rows, map[string]string{"deviceId": fmt.Sprintf("ap%d", i), "siteId": "floor"})
					}
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			guard := func(_ context.Context, sequence, name, cohort string, members []accessPointConfigurationMember) error {
				guards++
				if len(sequence) != 64 || len(cohort) != 64 || name != "Alpha" && name != "Beta" || name == "Alpha" && len(members) != 2 || name == "Beta" && (len(members) != 1 || members[0].DeviceID != "ap3") {
					t.Error("guard lost sequence/cohort/member identity")
				}
				if mode == "denied" {
					return errConfigurationDeviceAdmission
				}
				if mode == "late-other-target" {
					changed = true
				}
				if mode == "guard-copy" {
					members[0].EthernetMAC = "02:00:00:00:00:ff"
				}
				return nil
			}
			sequence, err := c.planAccessPointConfigurationSequence(ctx, wants, guard)
			if mode == "later-invalid" || mode == "cross-group-mac-alias" || mode == "planning-race" {
				if err == nil || sequence != nil || writes != 0 || guards != 0 {
					t.Fatal("invalid later cohort exposed a native plan", err)
				}
				return
			}
			if err != nil || sequence == nil || len(sequence.cohorts) != 3 {
				t.Fatal("missing complete sequence", err)
			}
			index := 1
			if mode == "selected-beta" {
				index = 2
			}
			if mode == "frozen" {
				wants[0].profile.payload["ledStatus"] = true
				wants[2].inventory.reference.Serial = "OTHER"
				sequence.cohorts[index].plan.members[0].EthernetMAC = "02:00:00:00:00:ff"
			}
			if mode == "before-other-target" {
				changed = true
			}
			var task Task
			if mode == "concurrent" {
				type outcome struct {
					index int
					task  Task
					err   error
				}
				results := make(chan outcome, 2)
				for _, selected := range []int{1, 2} {
					go func() {
						got, err := sequence.cohorts[selected].plan.submit(ctx)
						results <- outcome{selected, got, err}
					}()
				}
				accepted := 0
				for range 2 {
					got := <-results
					if got.err == nil {
						accepted++
						index, task = got.index, got.task
					}
				}
				if accepted != 1 {
					t.Fatal("concurrent group attempts must admit exactly one")
				}
			} else {
				task, err = sequence.cohorts[index].plan.submit(ctx)
			}
			valid := mode == "valid" || mode == "concurrent" || mode == "selected-beta" || mode == "reordered" || mode == "frozen" || mode == "guard-copy"
			if (err == nil) != valid || valid && task.ID != "task" {
				t.Fatal("unexpected sequence dispatch", err)
			}
			expectedWrites := 0
			if valid || mode == "lost-receipt" {
				expectedWrites = 1
			}
			if writes != expectedWrites {
				t.Fatal("unsafe write count", writes)
			}
			for _, cohort := range sequence.cohorts {
				if _, err := cohort.plan.submit(ctx); err == nil || writes != expectedWrites {
					t.Fatal("sequence allowed replay or another mutation without durable replanning")
				}
			}
			if expectedWrites == 1 {
				expected := []any{map[string]any{"macAddress": "02:00:00:00:00:01", "apNameNew": "AP1"}, map[string]any{"macAddress": "02:00:00:00:00:02", "apNameNew": "AP2"}}
				if index == 2 {
					expected = []any{map[string]any{"macAddress": "02:00:00:00:00:03", "apNameNew": "AP3"}}
				}
				if !reflect.DeepEqual(body["apList"], expected) || body["ledStatus"] != (index == 2) {
					t.Fatal("selected group body or settings changed", body)
				}
			}
			if guards > 1 || expectedWrites == 1 && guards != 1 {
				t.Fatal("sequence skipped or reacquired authority")
			}
		})
	}
}
