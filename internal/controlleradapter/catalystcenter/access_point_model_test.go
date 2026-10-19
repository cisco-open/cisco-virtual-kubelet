// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAccessPointProfileMapping(t *testing.T) {
	model := map[string]any{"name": "Office", "admin_status": false, "ap_mode": "LOCAL", "failover_priority": "CRITICAL", "led_status": true, "led_brightness_level": 8, "is_assigned_site_as_location": true, "primary_controller_name": "wlc", "primary_ip_address": "192.0.2.1", "radios": []any{map[string]any{"band": 5, "role": "SERVING", "admin_status": false, "antenna_pattern_name": "other", "antenna_gain": 10, "antenna_cable_name": "other", "cable_loss": 1.5, "channel_assignment_mode": "CUSTOM", "channel_number": 36, "channel_width": 80, "power_assignment_mode": "CUSTOM", "power_level": 3}}}
	before, _ := json.Marshal(model)
	profiles, err := parseAccessPointProfiles([]any{model})
	if err != nil || len(profiles) != 1 {
		t.Fatal("profile rejected", err)
	}
	p := profiles[0].payload
	for key, want := range map[string]any{"configureAdminStatus": true, "adminStatus": false, "apMode": 0, "failoverPriority": 4, "ledBrightnessLevel": float64(8), "configureLocation": true, "configureHAController": true, "primaryIpAddress": map[string]any{"address": "192.0.2.1"}} {
		if !reflect.DeepEqual(p[key], want) {
			t.Fatalf("wrong %s: %v", key, p[key])
		}
	}
	radio := p["radioConfigurations"].([]any)[0].(map[string]any)
	for key, want := range map[string]any{"radioType": 2, "radioBand": "RADIO5", "configureAdminStatus": true, "adminStatus": false, "channelWidth": 5, "channelAssignmentMode": 2, "powerAssignmentMode": 2, "configurePower": true, "cableLoss": 1.5} {
		if !reflect.DeepEqual(radio[key], want) {
			t.Fatalf("wrong radio %s: %v", key, radio[key])
		}
	}
	after, _ := json.Marshal(model)
	if string(before) != string(after) {
		t.Fatal("parser mutated source")
	}
	minimal, err := parseAccessPointProfiles([]any{map[string]any{"name": "minimal", "admin_status": nil, "is_assigned_site_as_location": false}})
	if err != nil || minimal[0].payload["configureAdminStatus"] != false || minimal[0].payload["configureLocation"] != false || minimal[0].payload["adminStatus"] != nil {
		t.Fatal("absent values enabled mutation", err)
	}
}

func TestAccessPointRadioRolesAndBands(t *testing.T) {
	for band, typ := range map[string]int{"2.4": 1, "5": 2, "6": 6, "XOR": 3} {
		for _, role := range []string{"AUTO", "SERVING", "MONITOR"} {
			p, err := parseAccessPointRadio(map[string]any{"band": band, "role": role})
			if err != nil || p["radioType"] != typ {
				t.Fatal("radio rejected", err)
			}
			if (p["radioBand"] != nil) != (role == "SERVING") {
				t.Fatal("AUTO/MONITOR incorrectly includes band")
			}
		}
	}
}

func TestAccessPointProfileRejectsInvalidIntent(t *testing.T) {
	for _, fields := range []map[string]any{
		{"primary_ip_address": "192.0.2.1"}, {"secondary_controller_name": "wlc2"}, {"secondary_ip_address": "192.0.2.2"}, {"tertiary_controller_name": "wlc3"}, {"tertiary_ip_address": "192.0.2.3"}, {"configure_admin_status": true}, {"unknown": nil}, {"admin_status": "true"}, {"ap_mode": "INVALID"}, {"failover_priority": 1},
		{"led_brightness_level": 0}, {"led_brightness_level": 9}, {"led_brightness_level": 1.5}, {"primary_ip_address": "example.test"},
		{"location": "room", "is_assigned_site_as_location": true}, {"radios": "5"}, {"radios": []any{map[string]any{"role": "SERVING"}}},
	} {
		model := map[string]any{"name": "test"}
		for k, v := range fields {
			model[k] = v
		}
		if p, err := parseAccessPointProfiles([]any{model}); err == nil || p != nil {
			t.Fatalf("invalid fields accepted: %v", fields)
		}
	}
	for _, radio := range []map[string]any{
		{"band": 7}, {"band": 5, "configure_power": true}, {"band": 5, "channel_number": 36}, {"band": 5, "role": "MONITOR", "power_level": 1},
		{"band": 5, "role": "SERVING", "power_level": 9}, {"band": 5, "antenna_gain": 21}, {"band": 5, "antenna_gain": 10},
		{"band": 5, "antenna_pattern_name": "internal", "antenna_gain": 10}, {"band": 5, "cable_loss": 1},
		{"band": 5, "role": "SERVING", "channel_width": 10}, {"band": 5, "role": "SERVING", "channel_number": 1.5},
	} {
		if p, err := parseAccessPointRadio(radio); err == nil || p != nil {
			t.Fatalf("invalid radio accepted: %v", radio)
		}
	}
	duplicate := map[string]any{"name": "test", "radios": []any{map[string]any{"band": 5}, map[string]any{"band": "5"}}}
	if _, err := parseAccessPointProfiles([]any{duplicate}); err == nil {
		t.Fatal("duplicate band accepted")
	}
	for _, name := range []string{"", "(hostname only)"} {
		if _, err := parseAccessPointProfiles([]any{map[string]any{"name": name}}); err == nil {
			t.Fatal("invalid profile name accepted")
		}
	}
	if _, err := parseAccessPointProfiles([]any{map[string]any{"name": "a"}, map[string]any{"name": "a"}}); err == nil {
		t.Fatal("duplicate profile name accepted")
	}
}
