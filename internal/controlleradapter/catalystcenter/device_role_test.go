// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func roleModel() []byte {
	return []byte(strings.Replace(inventoryAssignModel, `"state":"ASSIGN"`, `"state":"ASSIGN","device_role":"DISTRIBUTION","role_source":"MANUAL"`, 1))
}

func TestDeviceRoleExecutionAndIdentityRecovery(t *testing.T) {
	ctx := context.Background()
	device := Device{ID: "device", Hostname: "switch", ManagementIP: "198.51.100.101", Serial: "serial", Role: "ACCESS", RoleSource: "AUTO"}
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case devicesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []Device{device}})
		case deviceRolePath:
			if r.Method != "PUT" {
				t.Error("wrong role method")
			}
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 3 || body["id"] != "device" || body["role"] != "DISTRIBUTION" || body["roleSource"] != "MANUAL" {
				t.Error("unbound role payload")
			}
			writes++
			fmt.Fprint(w, `{"response":{"taskId":"role-task"}}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	intent, err := parseInventoryIntent(roleModel())
	if err != nil {
		t.Fatal(err)
	}
	e := &deviceRoleExecutor{client: c, desired: intent.devices}
	write, err := e.next(ctx)
	if err != nil || write == nil {
		t.Fatal("role plan", err)
	}
	op := &ciskov1.NetworkControllerConfigurationOperation{Action: write.action, Resource: write.resource, TargetID: write.identity.target, TargetSerial: write.identity.serial}
	task, err := write.submit(ctx)
	if err != nil || task.ID != "role-task" {
		t.Fatal("role receipt", err)
	}
	// Acceptance alone is not readback proof.
	if ok, err := e.verify(ctx, op); err != nil || ok {
		t.Fatal("premature verification", err)
	}
	device.Role, device.RoleSource = "DISTRIBUTION", "MANUAL"
	e = &deviceRoleExecutor{client: c, desired: intent.devices}
	if ok, err := e.verify(ctx, op); err != nil || !ok {
		t.Fatal("restart verification", err)
	}
	if next, err := e.next(ctx); err != nil || next != nil || writes != 1 {
		t.Fatal("role replay", err)
	}
	device.ID = "replacement"
	if ok, err := e.verify(ctx, op); err == nil || ok {
		t.Fatal("replaced UUID verified")
	}
	device.ID = "device"
	device.Serial = "different"
	if ok, err := e.verify(ctx, op); err == nil || ok {
		t.Fatal("replaced serial verified")
	}
}

func TestDeviceRoleValidationAndAdmission(t *testing.T) {
	for _, replacement := range [][2]string{
		{`"DISTRIBUTION"`, `"UNKNOWN"`}, {`"MANUAL"`, `"unknown"`},
		{`,"role_source":"MANUAL"`, ``}, {`,"device_role":"DISTRIBUTION"`, ``},
	} {
		if _, err := parseInventoryIntent([]byte(strings.Replace(string(roleModel()), replacement[0], replacement[1], 1))); err == nil {
			t.Fatal("unresolved role accepted")
		}
	}
	if _, err := (&client{}).compileConfigurationExecutor(roleModel(), "inventory"); err == nil {
		t.Fatal("role bypassed device admission")
	}
}

func TestDeviceRoleReport(t *testing.T) {
	role, source := "ACCESS", "AUTO"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Error("report attempted mutation")
		}
		switch r.URL.Path {
		case devicesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []Device{{ID: "device", Hostname: "switch", ManagementIP: "198.51.100.101", Serial: "serial", Role: role, RoleSource: source}}})
		case sitesPath:
			fmt.Fprint(w, `{"response":[{"id":"global","name":"Global","nameHierarchy":"Global","type":"global"},{"id":"site","name":"Lab","nameHierarchy":"Global/Lab","type":"area","parentId":"global"}]}`)
		case assignedDevicesPath:
			fmt.Fprint(w, `{"response":[{"deviceId":"device","siteId":"site"}]}`)
		default:
			t.Error("unexpected report path")
			w.WriteHeader(404)
		}
	})
	raw := []byte(strings.Replace(string(roleModel()), `,"tags":["lab"]`, "", 1))
	plans, err := c.compileConfigReports(raw, []string{"inventory"})
	if err != nil || plans[0].count != 2 {
		t.Fatal("role count", err)
	}
	drift, err := plans[0].observe(context.Background())
	if err != nil || len(drift) != 1 || drift[0].Path != "/device_role" {
		t.Fatal("missing role drift", drift, err)
	}
	role, source = "DISTRIBUTION", "MANUAL"
	if drift, err = plans[0].observe(context.Background()); err != nil || len(drift) != 0 {
		t.Fatal("converged role drift", drift, err)
	}
	source = ""
	if _, err = plans[0].observe(context.Background()); err == nil {
		t.Fatal("missing role source treated as valid observation")
	}
}

func TestDeviceRolePreflightRejectsPartialAndUnknownInventory(t *testing.T) {
	for _, mode := range []string{"duplicate", "missing-serial", "missing-role", "unresolved"} {
		t.Run(mode, func(t *testing.T) {
			d := Device{ID: "device", Hostname: "switch", Serial: "serial", ManagementIP: "198.51.100.101", Role: "ACCESS", RoleSource: "MANUAL"}
			if mode == "missing-role" {
				d.Role = ""
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != devicesPath {
					t.Error("unexpected mutation or endpoint")
				}
				json.NewEncoder(w).Encode(map[string]any{"response": []Device{d}})
			})
			intent, err := parseInventoryIntent(roleModel())
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "duplicate":
				intent.devices = append(intent.devices, intent.devices[0])
			case "missing-serial":
				intent.devices[0].reference.Serial = ""
			case "unresolved":
				extra := intent.devices[0]
				extra.reference = configurationDeviceReference{Name: "absent", Serial: "absent"}
				intent.devices = append(intent.devices, extra)
			}
			if write, err := (&deviceRoleExecutor{client: c, desired: intent.devices}).next(context.Background()); err == nil || write != nil {
				t.Fatal("partial or unidentified role update released")
			}
		})
	}
}
