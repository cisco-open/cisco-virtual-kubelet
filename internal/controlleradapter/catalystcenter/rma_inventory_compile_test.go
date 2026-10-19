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

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

func TestRMAMarkInventoryCompiler(t *testing.T) {
	for _, mode := range []string{"plan", "two-devices", "unprovisioned-second", "unowned-second", "late-second", "replace", "mixed", "role", "no-serial"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			raw := strings.Replace(rmaModel(`{"action":"MARK_FOR_REPLACEMENT"}`), "FOC12345678", "OLD-SERIAL", 1)
			multiple := mode == "two-devices" || strings.HasSuffix(mode, "-second") || mode == "mixed"
			if multiple {
				var root map[string]any
				if err := json.Unmarshal([]byte(raw), &root); err != nil {
					t.Fatal(err)
				}
				inventory := root["catalyst_center"].(map[string]any)["inventory"].(map[string]any)
				rows := inventory["devices"].([]any)
				second := map[string]any{"name": "other", "serial_number": "OTHER-SERIAL", "site": "Global/Lab/Building", "state": "PROVISION", "rma": map[string]any{"action": rmaMark}}
				if mode == "mixed" {
					delete(second, "rma")
				}
				inventory["devices"] = append(rows, second)
				encoded, err := json.Marshal(root)
				if err != nil {
					t.Fatal(err)
				}
				raw = string(encoded)
			}
			if mode == "replace" {
				raw = strings.Replace(raw, `"MARK_FOR_REPLACEMENT"`, `"REPLACE","replacement_serial_number":"SPARE"`, 1)
			}
			if mode == "role" {
				raw = strings.Replace(raw, `"name":"edge"`, `"name":"edge","device_role":"ACCESS","role_source":"MANUAL"`, 1)
			}
			if mode == "no-serial" {
				raw = strings.Replace(raw, `,"serial_number":"OLD-SERIAL"`, "", 1)
			}
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.Source.Inline.Raw = []byte(raw)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.compileRMAMarkInventoryApply(config, source); err == nil {
				t.Fatal("missing admission accepted")
			}
			a.workerPodName, a.workerPodUID = "connector", "connector-uid"
			if err := a.ConfigureConfigurationReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
				t.Fatal(err)
			}
			plan, err := a.compileRMAMarkInventoryApply(config, source)
			invalid := mode == "replace" || mode == "mixed" || mode == "role" || mode == "no-serial"
			if invalid {
				if err == nil {
					t.Fatal("partially supported intent compiled")
				}
				if _, err := a.compileAdmittedInventoryApply(config, source); err == nil {
					t.Fatal("public router accepted partial RMA intent")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := a.claimConfigurationSection(ctx, config, source); err != nil {
				t.Fatal(err)
			}
			late := false
			posts := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					posts++
					fmt.Fprint(w, `{"response":{"taskId":"mark-task"}}`)
					return
				}
				var rows any
				switch r.URL.Path {
				case devicesPath:
					devices := []Device{{ID: "old-id", Hostname: "edge", Serial: "OLD-SERIAL", Reachability: "Unreachable"}}
					if multiple {
						serial := "OTHER-SERIAL"
						if late {
							serial = "REPLACED-SERIAL"
						}
						devices = append(devices, Device{ID: "other-id", Hostname: "other", Serial: serial})
					}
					rows = devices
				case sitesPath:
					rows = sampleSites()
				case assignedDevicesPath:
					rows = []map[string]string{{"deviceId": "old-id", "siteId": "building"}, {"deviceId": "other-id", "siteId": "building"}}
				case wiredProvisionPath:
					id := r.URL.Query().Get("networkDeviceId")
					records := []wiredProvisionedDevice{}
					if !(mode == "unprovisioned-second" && id == "other-id") {
						records = append(records, wiredProvisionedDevice{ID: id + "-provision", SiteID: "building", DeviceID: id})
					}
					rows = records
				case deviceReplacementPath:
					records := []deviceReplacementRecord{}
					if mode == "unowned-second" {
						records = append(records, deviceReplacementRecord{ID: "foreign", FaultyID: "other-id", FaultySerial: "OTHER-SERIAL", Status: "MARKED-FOR-REPLACEMENT"})
					}
					rows = records
				default:
					t.Error("unexpected endpoint")
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			write, err := plan.executor.next(ctx)
			refused := mode == "unprovisioned-second" || mode == "unowned-second"
			if (err == nil) == refused || (write != nil) == refused || posts != 0 {
				t.Fatal("incomplete composition preflight", err, posts)
			}
			if refused {
				return
			}
			if plan.recordCompletion == nil || plan.observeCompletion == nil || write.identity.parent != "building" {
				t.Fatal("lost completion or physical binding")
			}
			count := 1
			if multiple {
				count = 2
			}
			if plan.executor.count() != count {
				t.Fatal("wrong resource count")
			}
			if mode == "late-second" {
				late = true
				if _, err := write.submit(ctx); err == nil || posts != 0 {
					t.Fatal("changed later target allowed first write")
				}
			}
			if _, err := a.compileAdmittedInventoryApply(config, source); err != nil {
				t.Fatal("public RMA mark route rejected", err)
			}
		})
	}
}
