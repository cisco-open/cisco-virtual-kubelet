// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"testing"
)

func TestWirelessDeviceProvisionSubmission(t *testing.T) {
	for _, scenario := range []string{"PROVISION", "REPROVISION", "replace-id", "replace-serial", "replace-ip", "wrong-family", "unreachable", "missing-task", "empty-accepted", "status-changed"} {
		t.Run(scenario, func(t *testing.T) {
			device := Device{ID: "wlc-id", Hostname: "wlc", Serial: "serial", ManagementIP: "198.51.100.10", Family: "Wireless Controller", Reachability: "Reachable"}
			writes := 0
			stamp := "100"
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case devicesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []Device{device}})
				case "/dna/intent/api/v1/wirelessControllers/wlc-id/provisionStatus":
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"networkDeviceId": "wlc-id", "deviceName": "wlc", "siteId": "site", "networkIntentProvisionStatus": "SUCCESS", "modelConfigProvisionStatus": "NOT_STARTED", "outOfSync": false, "lastProvisionedTimeStamp": stamp}})
				case "/dna/intent/api/v1/wirelessControllers/wlc-id/provision":
					writes++
					var body map[string]any
					if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil || body == nil || len(body) != 0 {
						t.Error("wrong provisioning request")
					}
					if scenario == "empty-accepted" {
						w.WriteHeader(202)
						return
					}
					if scenario == "missing-task" {
						fmt.Fprint(w, `{"response":{}}`)
						return
					}
					fmt.Fprint(w, `{"response":{"taskId":"provision-task"}}`)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			state := "PROVISION"
			if scenario == "REPROVISION" {
				state = scenario
			}
			write, err := c.planWirelessDeviceProvision(context.Background(), configurationDeviceReference{Name: "wlc", Serial: "serial"}, state, "site", false)
			if err != nil || write == nil {
				t.Fatal("planning", err)
			}
			recovered, err := restoreWirelessProvisionBaseline("wlc", &cvk.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, TargetSerial: write.identity.serial})
			if err != nil || recovered.lastProvisioned != "100" || recovered.siteID != "site" {
				t.Fatal("planner lost baseline", err)
			}
			if write.identity.target != "wlc-id" || write.identity.serial != "serial" || state == "REPROVISION" && write.action != "Update" {
				t.Fatal("unbound operation")
			}
			switch scenario {
			case "status-changed":
				stamp = "101"
			case "replace-id":
				device.ID = "new-id"
			case "replace-serial":
				device.Serial = "new-serial"
			case "replace-ip":
				device.ManagementIP = "198.51.100.11"
			case "wrong-family":
				device.Family = "Switches and Hubs"
			case "unreachable":
				device.Reachability = "Unreachable"
			}
			task, err := write.submit(context.Background())
			valid := scenario == "PROVISION" || scenario == "REPROVISION"
			if valid && (err != nil || task.ID != "provision-task" || writes != 1) {
				t.Fatal("submission", task, err, writes)
			}
			if !valid && (err == nil || task.ID != "") {
				t.Fatal("unsafe acceptance", task, err)
			}
			if !valid && scenario != "missing-task" && scenario != "empty-accepted" && writes != 0 {
				t.Fatal("changed device submitted")
			}
		})
	}
}
func TestWirelessDeviceProvisionRejectsUnboundIntent(t *testing.T) {
	for _, state := range []string{"", "ASSIGN", "provision"} {
		if _, err := (&client{}).planWirelessDeviceProvision(context.Background(), configurationDeviceReference{Name: "wlc", Serial: "serial"}, state, "site", false); err == nil {
			t.Fatal("invalid state")
		}
	}
	if _, err := (&client{}).planWirelessDeviceProvision(context.Background(), configurationDeviceReference{Name: "wlc"}, "PROVISION", "site", false); err == nil {
		t.Fatal("missing serial")
	}
}

func TestWirelessProvisionPlanningRejectsUnstableBaseline(t *testing.T) {
	for _, scenario := range []string{"running", "wrong-site", "missing-stamp"} {
		t.Run(scenario, func(t *testing.T) {
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case devicesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []Device{{ID: "wlc", Serial: "serial", Hostname: "WLC", ManagementIP: "198.51.100.10", Family: "Wireless Controller", Reachability: "Reachable"}}})
				case "/dna/intent/api/v1/wirelessControllers/wlc/provisionStatus":
					row := map[string]any{"networkDeviceId": "wlc", "deviceName": "WLC", "siteId": "site", "networkIntentProvisionStatus": "SUCCESS", "modelConfigProvisionStatus": "NOT_STARTED", "outOfSync": false, "lastProvisionedTimeStamp": "100"}
					switch scenario {
					case "running":
						row["networkIntentProvisionStatus"] = "CONFIGURING"
					case "wrong-site":
						row["siteId"] = "other"
					case "missing-stamp":
						delete(row, "lastProvisionedTimeStamp")
					}
					json.NewEncoder(w).Encode(map[string]any{"response": row})
				default:
					writes++
					w.WriteHeader(500)
				}
			})
			write, err := c.planWirelessDeviceProvision(context.Background(), configurationDeviceReference{Name: "WLC", Serial: "serial"}, "REPROVISION", "site", false)
			if err == nil || write != nil || writes != 0 {
				t.Fatal("unstable baseline allowed", err, writes)
			}
		})
	}
}
