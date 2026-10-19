// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"testing"
)

func TestAccessPointDeviceProfileBinding(t *testing.T) {
	profiles, err := parseAccessPointProfiles([]any{map[string]any{"name": "office", "led_status": false}})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"provision", "reprovision", "rename-only", "settings-only", "assign", "unknown-profile", "unknown-field", "non-ap", "rename-no-serial", "assign-settings", "no-rf", "invalid-site", "invalid-ip", "override", "bad-flag"} {
		t.Run(mode, func(t *testing.T) {
			block := map[string]any{"configuration": "office", "manage_hostname": true}
			model := map[string]any{"name": "ap-new", "serial_number": "SERIAL", "device_ip": "192.0.2.4", "type": "AccessPoint", "site": "Global/Lab/Building/Floor", "state": "PROVISION", "rf_profile": "TYPICAL", "tags": []any{"office"}, "access_point": block}
			switch mode {
			case "reprovision":
				model["state"] = "REPROVISION"
			case "rename-only":
				delete(block, "configuration")
			case "settings-only":
				block["manage_hostname"] = false
				delete(model, "serial_number")
			case "assign":
				model["state"] = "ASSIGN"
				delete(model, "access_point")
				delete(model, "rf_profile")
			case "unknown-profile":
				block["configuration"] = "missing"
			case "unknown-field":
				model["unimplemented"] = true
			case "non-ap":
				model["type"] = "WirelessController"
			case "rename-no-serial":
				delete(model, "serial_number")
			case "assign-settings":
				model["state"] = "ASSIGN"
			case "no-rf":
				delete(model, "rf_profile")
			case "invalid-site":
				model["site"] = "unqualified"
			case "invalid-ip":
				model["device_ip"] = "controller.example"
			case "override":
				block["led_status"] = true
			case "bad-flag":
				block["manage_hostname"] = "true"
			}
			before, _ := json.Marshal(model)
			got, err := parseAccessPointDevice(model, profiles)
			valid := mode == "provision" || mode == "reprovision" || mode == "rename-only" || mode == "settings-only" || mode == "assign"
			if (err == nil) != valid {
				t.Fatalf("binding error=%v", err)
			}
			after, _ := json.Marshal(model)
			if string(before) != string(after) {
				t.Fatal("binding changed source")
			}
			if !valid {
				return
			}
			if got.inventory.reference.Name != "ap-new" || got.inventory.site != "Global/Lab/Building/Floor" || len(got.inventory.tags) != 1 || got.state != model["state"] {
				t.Fatal("lost shared inventory fields")
			}
			if mode == "rename-only" && (got.profile != nil || !got.manageHostname) {
				t.Fatal("rename-only inherited a profile")
			}
			if mode == "provision" && (got.profile == nil || got.profile.name != "office" || got.profile.payload["ledStatus"] != false || !got.manageHostname) {
				t.Fatal("selected profile lost")
			}
			if mode == "settings-only" && got.manageHostname {
				t.Fatal("settings-only enabled rename")
			}
			if _, err := parseInventoryIntent([]byte(`{"catalyst_center":{"inventory":{"devices":[` + string(before) + `]}}}`)); err == nil {
				t.Fatal("generic inventory prematurely enabled AP execution")
			}
		})
	}
}
