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
	"time"
)

func TestAccessPointConfigurationExecutionObservation(t *testing.T) {
	for _, mode := range []string{"valid", "hostname-only", "settings-not-applied", "running", "failed-child", "disconnected-tree", "future-task", "changed-task-end", "late-child-failure", "late-target", "denied", "late-denied", "wrong-digest", "replaced-ap", "wrong-mac", "missing-readback", "changed-readback", "unknown-provisioning-status", "guard-copy", "missing-guard", "invalid-task", "missing-submission", "future-submission"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Millisecond)
			submitted, completed := now.Add(-time.Minute), now.Add(-10*time.Second)
			profiles, err := parseAccessPointProfiles([]any{map[string]any{"name": "standard", "led_status": false}})
			if err != nil {
				t.Fatal(err)
			}
			declare := func() []accessPointDeviceIntent {
				t.Helper()
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
				return wants
			}
			observing, changed := false, false
			trees, guards := 0, 0
			readbacks := map[string]int{}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("execution observation mutated the controller")
					w.WriteHeader(400)
					return
				}
				rows := []any{}
				switch r.URL.Path {
				case devicesPath:
					for i := 1; i <= 2; i++ {
						id := fmt.Sprintf("ap%d", i)
						if observing && i == 2 && (mode == "replaced-ap" || changed && mode == "late-target") {
							id = "replacement"
						}
						rows = append(rows, Device{ID: id, Serial: fmt.Sprintf("SERIAL%d", i), Hostname: fmt.Sprintf("AP%d", i), PlatformID: "C9130AXI", APEthernetMAC: fmt.Sprintf("02:00:00:00:00:0%d", i)})
					}
				case sitesPath:
					for _, site := range append(sampleSites(), Site{ID: "floor", Name: "Floor", NameHierarchy: "Global/Lab/Building/Floor", ParentID: "building", Type: "floor"}) {
						rows = append(rows, site)
					}
				case wirelessRFPath:
					rows = append(rows, map[string]any{"id": "rf", "rfProfileName": "TYPICAL", "defaultRfProfile": true, "enableRadioTypeA": true, "enableRadioTypeB": true, "enableRadioType6GHz": false})
				case assignedDevicesPath:
					for i := 1; i <= 2; i++ {
						rows = append(rows, map[string]string{"deviceId": fmt.Sprintf("ap%d", i), "siteId": "floor"})
					}
				case taskPath + "task/tree":
					trees++
					end := completed.UnixMilli()
					if mode == "future-task" {
						end = now.Add(time.Second).UnixMilli()
					}
					if mode == "changed-task-end" && trees > 1 {
						end++
					}
					root := map[string]any{"id": "task", "startTime": submitted.UnixMilli(), "endTime": end, "isError": false}
					child := map[string]any{"id": "child", "parentId": "task", "rootId": "task", "startTime": submitted.UnixMilli(), "endTime": end, "isError": mode == "failed-child" || mode == "late-child-failure" && changed}
					if mode == "running" {
						delete(child, "endTime")
					}
					if mode == "disconnected-tree" {
						child["parentId"] = "foreign"
					}
					rows = append(rows, root, child)
				case accessPointSummaryPath:
					mac := r.URL.Query().Get("key")
					if len(r.URL.Query()) != 1 || mac != "02:00:00:00:00:01" && mac != "02:00:00:00:00:02" {
						t.Error("unbounded or foreign AP readback")
					}
					readbacks[mac]++
					if mode == "missing-readback" && strings.HasSuffix(mac, "02") {
						w.WriteHeader(404)
						return
					}
					observedMAC, status, name, led := mac, "opaque-native-status", "AP1", "Disabled"
					if strings.HasSuffix(mac, "02") {
						name = "AP2"
						if mode == "wrong-mac" {
							observedMAC = "02:00:00:00:00:03"
						}
						if mode == "changed-readback" && readbacks[mac] > 1 {
							name = "changed"
						}
					}
					if mode == "settings-not-applied" {
						led = "Enabled"
					}
					json.NewEncoder(w).Encode(map[string]any{"ethMac": observedMAC, "apName": name, "provisioningStatus": status, "ledStatus": led})
					return
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			frozen, err := freezeAccessPointConfiguration(declare())
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := c.accessPointConfigurationSnapshot(ctx, frozen)
			if err != nil {
				t.Fatal(err)
			}
			// Drop the planning objects: recovery uses fresh declared intent and
			// the retained digest/task metadata, never a submission closure.
			digest := snapshot.digest
			if mode == "wrong-digest" {
				digest = strings.Repeat("0", 64)
			}
			observing = true
			guard := func(_ context.Context, got string, members []accessPointConfigurationMember) error {
				guards++
				if got != snapshot.digest || len(members) != 2 || members[0].DeviceID != "ap1" || members[1].DeviceID != "ap2" {
					t.Error("observation guard lost retained cohort binding")
				}
				if mode == "denied" || mode == "late-denied" && guards > 1 {
					return errConfigurationStale
				}
				if guards == 3 {
					changed = true
				}
				if mode == "guard-copy" {
					members[0].EthernetMAC = "02:00:00:00:00:ff"
				}
				return nil
			}
			taskID := "task"
			switch mode {
			case "missing-guard":
				guard = nil
			case "invalid-task":
				taskID = "../task"
			case "missing-submission":
				submitted = time.Time{}
			case "future-submission":
				submitted = now.Add(time.Second)
			}
			compliance, err := c.observeAccessPointConfigurationCompliance(ctx, declare(), digest, taskID, submitted, now, guard)
			var result *accessPointConfigurationExecutionObservation
			if compliance != nil {
				result = compliance.execution
				if len(compliance.settings) != 2 {
					t.Fatal("member setting comparison was omitted")
				}
				for _, settings := range compliance.settings {
					if accessPointSettingsMatch(settings) != (mode == "hostname-only") {
						t.Fatal("unknown setting representation supplied false convergence", settings)
					}
				}
			}
			valid := mode == "valid" || mode == "hostname-only" || mode == "settings-not-applied" || mode == "unknown-provisioning-status" || mode == "guard-copy"
			if mode == "running" {
				if err != nil || result != nil || len(readbacks) != 0 {
					t.Fatal("running task produced execution evidence", err)
				}
				return
			}
			if !valid {
				if err == nil || result != nil {
					t.Fatal("stale, failed or denied observation returned evidence", mode, err)
				}
				return
			}
			if err != nil || result == nil || !result.taskCompletedAt.Equal(completed) || len(result.members) != 2 || trees != 2 || guards != 3 {
				t.Fatal("missing exact execution observation", err)
			}
			for _, row := range result.members {
				if row.ProvisioningStatus == nil || *row.ProvisioningStatus != "opaque-native-status" || readbacks[row.EthernetMAC] != 2 {
					t.Fatal("unknown native state was reinterpreted or member readback was skipped")
				}
				if mode == "settings-not-applied" && (row.LEDStatus == nil || *row.LEDStatus != "Enabled") {
					t.Fatal("task success was substituted for actual selected-setting readback")
				}
			}
		})
	}
}
