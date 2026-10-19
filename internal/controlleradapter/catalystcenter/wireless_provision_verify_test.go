// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"testing"
	"time"
)

func TestWirelessProvisionVerification(t *testing.T) {
	for _, scenario := range []string{"success", "old-success", "missing-stamp", "task-running", "task-stale", "task-failed", "wrong-site", "wrong-device", "out-of-sync", "model-required", "model-failed", "rollback"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now()
			submitted := metav1.NewTime(now.Add(-time.Minute))
			d := Device{ID: "wlc", Serial: "serial", Hostname: "WLC", ManagementIP: "198.51.100.10", Family: "Wireless Controller", Reachability: "Reachable"}
			baseline := wirelessProvisionBaseline{device: d, resource: "provision-resource", siteID: "site", lastProvisioned: "100"}
			op := &cvk.NetworkControllerConfigurationOperation{Resource: baseline.resource, Action: "Update", Phase: "Verifying", TargetID: d.ID, TargetSerial: d.Serial, TaskID: "task", SubmittedAt: &submitted}
			status := map[string]any{"networkDeviceId": "wlc", "deviceName": "WLC", "siteId": "site", "networkIntentProvisionStatus": "SUCCESS", "modelConfigProvisionStatus": "NOT_STARTED", "outOfSync": false, "lastProvisionedTimeStamp": "101"}
			switch scenario {
			case "old-success":
				status["lastProvisionedTimeStamp"] = "100"
			case "missing-stamp":
				delete(status, "lastProvisionedTimeStamp")
			case "wrong-site":
				status["siteId"] = "other"
			case "wrong-device":
				d.ID = "replacement"
			case "out-of-sync":
				status["outOfSync"] = true
			case "model-required":
				baseline.modelConfigRequired = true
			case "model-failed":
				status["modelConfigProvisionStatus"] = "FAILED"
			case "rollback":
				status["networkIntentProvisionStatus"] = "ROLLBACK_SUCCESS"
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" && r.URL.Path != authPath {
					t.Error("verifier mutated device")
				}
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case taskPath + "task":
					start, end := submitted.UnixMilli(), now.UnixMilli()
					if scenario == "task-running" {
						end = 0
					}
					if scenario == "task-stale" {
						start = submitted.Add(-time.Hour).UnixMilli()
					}
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "task", "startTime": start, "endTime": end, "isError": scenario == "task-failed"}})
				case devicesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []Device{d}})
				case "/dna/intent/api/v1/wirelessControllers/wlc/provisionStatus":
					json.NewEncoder(w).Encode(map[string]any{"response": status})
				default:
					t.Error("unexpected path", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			record, err := wirelessProvisionRecordFor("WLC", op.Action, baseline)
			if err != nil {
				t.Fatal(err)
			}
			op.Resource, err = record.resource()
			if err != nil {
				t.Fatal(err)
			}
			ok, err := c.verifyRecordedWirelessProvision(context.Background(), "WLC", op, now)
			if scenario == "success" {
				if !ok || err != nil {
					t.Fatal("valid verification", ok, err)
				}
			} else {
				if ok {
					t.Fatal("premature completion", scenario)
				}
				wantError := scenario == "task-stale" || scenario == "task-failed" || scenario == "wrong-site" || scenario == "wrong-device" || scenario == "model-failed" || scenario == "rollback"
				if (err != nil) != wantError {
					t.Fatal("wrong failure/pending classification", scenario, err)
				}
			}
		})
	}
}
