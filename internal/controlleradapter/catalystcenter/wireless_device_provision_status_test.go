// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestWirelessDeviceProvisionStatus(t *testing.T) {
	for _, state := range []string{"NOT_STARTED", "SUCCESS", "FAILED", "CONFIGURING", "ROLLBACK_SUCCESS", "ROLLBACK_FAILURE", "ABORTED", "PENDING"} {
		t.Run(state, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != "/dna/intent/api/v1/wirelessControllers/wlc/provisionStatus" {
					t.Error("wrong status request")
				}
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"networkDeviceId": "wlc", "deviceName": "WLC", "networkIntentProvisionStatus": state, "modelConfigProvisionStatus": "NOT_STARTED", "outOfSync": false, "lastProvisionedTimeStamp": "1790000000000", "siteId": "site", "siteNameHierarchy": "Global/Lab"}})
			})
			got, err := c.wirelessDeviceProvisionStatus(context.Background(), "wlc")
			if err != nil || got.networkIntent != state || got.modelConfig != "NOT_STARTED" || got.lastProvisioned != "1790000000000" || got.siteID != "site" {
				t.Fatal(got, err)
			}
		})
	}
}
func TestWirelessDeviceProvisionStatusRejectsInvalidResponse(t *testing.T) {
	for _, field := range []string{"networkDeviceId", "deviceName", "networkIntentProvisionStatus", "modelConfigProvisionStatus", "outOfSync", "timestamp", "timestamp-type", "unknown-status", "bad-boolean", "null-response"} {
		t.Run(field, func(t *testing.T) {
			row := map[string]any{"networkDeviceId": "wlc", "deviceName": "WLC", "networkIntentProvisionStatus": "SUCCESS", "modelConfigProvisionStatus": "SUCCESS", "outOfSync": false}
			switch field {
			case "unknown-status":
				row["networkIntentProvisionStatus"] = "COMPLETE"
			case "bad-boolean":
				row["outOfSync"] = "false"
			case "timestamp":
				row["lastProvisionedTimeStamp"] = "yesterday"
			case "timestamp-type":
				row["lastProvisionedTimeStamp"] = 123
			case "null-response":
				row = nil
			case "networkDeviceId":
				row[field] = "replacement"
			default:
				delete(row, field)
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": row})
			})
			if _, err := c.wirelessDeviceProvisionStatus(context.Background(), "wlc"); err == nil {
				t.Fatal("invalid status accepted")
			}
		})
	}
	if _, err := (&client{}).wirelessDeviceProvisionStatus(context.Background(), "../bad"); err == nil {
		t.Fatal("invalid ID accepted")
	}
}
