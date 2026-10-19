// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestDeviceAssignmentExecutionRecovery(t *testing.T) {
	assigned := false
	siteID, deviceID, serial := "site", "device", "serial"
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		switch r.URL.Path {
		case devicesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []Device{{ID: deviceID, Hostname: "switch", ManagementIP: "198.51.100.101", Serial: serial}}})
		case sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}, {ID: siteID, Name: "Lab", NameHierarchy: "Global/Lab", Type: "area", ParentID: "global"}}})
		case assignedDevicesPath:
			if r.URL.Query().Get("siteId") != siteID {
				t.Error("wrong site")
			}
			if assigned {
				fmt.Fprintf(w, `{"response":[{"deviceId":%q,"siteId":%q}]}`, deviceID, siteID)
			} else {
				fmt.Fprint(w, `{"response":[]}`)
			}
		case assignDevicesPath:
			if r.Method != "POST" {
				t.Error("wrong mutation method")
			}
			var body struct {
				DeviceIDs []string `json:"deviceIds"`
				SiteID    string   `json:"siteId"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.DeviceIDs) != 1 || body.DeviceIDs[0] != deviceID || body.SiteID != siteID {
				t.Error("assignment payload did not bind exact device and site")
			}
			writes++
			assigned = true
			fmt.Fprint(w, `{"response":{"taskId":"assignment-task"}}`)
		default:
			t.Error("unexpected API")
			w.WriteHeader(404)
		}
	})
	intent, err := parseInventoryIntent([]byte(inventoryAssignModel))
	if err != nil {
		t.Fatal(err)
	}
	executor := &deviceAssignmentExecutor{client: c, desired: intent.devices}
	bad := &deviceAssignmentExecutor{client: c, desired: append(append([]inventoryDeviceIntent{}, intent.devices...), inventoryDeviceIntent{reference: configurationDeviceReference{Name: "absent", Serial: "absent"}, site: "Global/Lab"})}
	if write, err := bad.next(context.Background()); err == nil || write != nil {
		t.Fatal("partial assignment released before complete preflight")
	}
	write, err := executor.next(context.Background())
	if err != nil || write == nil {
		t.Fatal("assignment not planned", err)
	}
	op := &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent, TargetSerial: write.identity.serial}
	if ok, err := executor.verify(context.Background(), op); err != nil || ok {
		t.Fatal("unassigned device considered complete", err)
	}
	task, err := write.submit(context.Background())
	if err != nil || task.ID != "assignment-task" {
		t.Fatal("missing receipt", err)
	}
	// Reconstruct resource executor to model worker recovery with no local state.
	executor = &deviceAssignmentExecutor{client: c, desired: intent.devices}
	if ok, err := executor.verify(context.Background(), op); err != nil || !ok {
		t.Fatal("assignment readback failed", err)
	}
	if next, err := executor.next(context.Background()); err != nil || next != nil || writes != 1 {
		t.Fatal("converged assignment replayed", err)
	}
	legacy := op.DeepCopy()
	legacy.TargetSerial = ""
	if ok, err := executor.verify(context.Background(), legacy); err == nil || ok {
		t.Fatal("unbound legacy device operation verified")
	}
	for _, replacement := range []string{"site", "device", "serial"} {
		siteID, deviceID, serial = "site", "device", "serial"
		switch replacement {
		case "site":
			siteID = "replacement"
		case "device":
			deviceID = "replacement"
		case "serial":
			serial = "replacement"
		}
		if ok, err := executor.verify(context.Background(), op); err == nil || ok {
			t.Fatal("replacement identity accepted", replacement)
		}
	}
}
