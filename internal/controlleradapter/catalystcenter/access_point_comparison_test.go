// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAccessPointSelectedSettingCoverage(t *testing.T) {
	profiles, err := parseAccessPointProfiles([]any{map[string]any{
		"name": "standard", "admin_status": false, "ap_mode": "LOCAL", "failover_priority": "LOW", "led_status": false,
		"led_brightness_level": 3, "location": "rack-1", "is_assigned_site_as_location": false,
		"primary_controller_name": "primary", "primary_ip_address": "2001:db8::1",
		"secondary_controller_name": "secondary", "secondary_ip_address": "192.0.2.2",
		"tertiary_controller_name": "tertiary", "tertiary_ip_address": "192.0.2.3",
		"radios": []any{map[string]any{"band": "5", "admin_status": false, "role": "SERVING", "antenna_pattern_name": "other", "antenna_gain": 0, "antenna_cable_name": "other", "cable_loss": 3, "channel_assignment_mode": "CUSTOM", "channel_number": 36, "channel_width": "80", "power_assignment_mode": "CUSTOM", "power_level": 3}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := accessPointDeviceIntent{profile: &profiles[0], manageHostname: true, inventory: inventoryDeviceIntent{reference: configurationDeviceReference{Name: "AP1"}}}
	var row accessPointObservation
	if err := json.Unmarshal([]byte(`{"ethMac":"02:00:00:00:00:01","apName":"AP1","adminStatus":"Disabled","apMode":"Local","failoverPriority":"Low","ledStatus":"Disabled","ledBrightnessLevel":3,"location":"rack-1","primaryControllerName":"primary","primaryIpAddress":"2001:0db8:0:0:0:0:0:1","secondaryControllerName":"secondary","secondaryIpAddress":"192.0.2.2","tertiaryControllerName":"tertiary","tertiaryIpAddress":"192.0.2.3","radioDTOs":[{"ifType":2,"slotId":0,"antennaPatternName":"other","antennaGain":0,"channelNumber":36,"powerlevel":3,"radioRoleAssignment":{"opaque":"value"}}]}`), &row); err != nil {
		t.Fatal(err)
	}
	comparisons, err := compareAccessPointSettings(want, row)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]accessPointSettingState{}
	for _, path := range []string{"/apNameNew", "/ledBrightnessLevel", "/location", "/primaryControllerName", "/primaryIpAddress", "/secondaryControllerName", "/secondaryIpAddress", "/tertiaryControllerName", "/tertiaryIpAddress", "/radioConfigurations/2/antennaPatternName", "/radioConfigurations/2/antennaGain", "/radioConfigurations/2/channelNumber", "/radioConfigurations/2/powerLevel"} {
		expected[path] = apSettingMatched
	}
	for _, path := range []string{"/adminStatus", "/apMode", "/failoverPriority", "/ledStatus", "/radioConfigurations/2/adminStatus", "/radioConfigurations/2/antennaCableName", "/radioConfigurations/2/cableLoss", "/radioConfigurations/2/channelAssignmentMode", "/radioConfigurations/2/channelWidth", "/radioConfigurations/2/powerAssignmentMode", "/radioConfigurations/2/radioRoleAssignment", "/radioConfigurations/2/radioBand"} {
		expected[path] = apSettingUnavailable
	}
	actual := map[string]accessPointSettingState{}
	for _, comparison := range comparisons {
		if _, duplicate := actual[comparison.path]; duplicate {
			t.Fatal("duplicate setting classification", comparison.path)
		}
		actual[comparison.path] = comparison.state
	}
	if !reflect.DeepEqual(actual, expected) || accessPointSettingsMatch(comparisons) {
		t.Fatal("selected setting omitted or undocumented representation accepted", actual)
	}
}

func TestAccessPointComparisonDoesNotInventConvergence(t *testing.T) {
	for _, mode := range []string{"matched", "mismatch", "missing-field", "missing-radio", "duplicate-radio-type", "wrong-radio-type", "unknown-field", "unknown-control-field", "assigned-site-location", "rename", "no-rename", "empty-selection", "bad-native-identity"} {
		t.Run(mode, func(t *testing.T) {
			model := map[string]any{"name": "standard", "led_brightness_level": 3, "location": "rack-1", "primary_controller_name": "primary", "primary_ip_address": "2001:db8::1"}
			if mode == "assigned-site-location" {
				delete(model, "location")
				model["is_assigned_site_as_location"] = true
			}
			if mode == "empty-selection" || mode == "rename" || mode == "no-rename" {
				model = map[string]any{"name": "standard"}
			}
			if mode == "missing-radio" || mode == "duplicate-radio-type" || mode == "wrong-radio-type" {
				model["radios"] = []any{map[string]any{"band": "5", "antenna_pattern_name": "antenna"}}
			}
			profiles, err := parseAccessPointProfiles([]any{model})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unknown-field" {
				profiles[0].payload["newNativeSetting"] = "value"
			}
			if mode == "unknown-control-field" {
				profiles[0].payload["radioType"] = 2
			}
			want := accessPointDeviceIntent{profile: &profiles[0], manageHostname: mode == "rename", inventory: inventoryDeviceIntent{reference: configurationDeviceReference{Name: "AP1"}}}
			var row accessPointObservation
			if err := json.Unmarshal([]byte(`{"ethMac":"02:00:00:00:00:01","apName":"AP1","ledBrightnessLevel":3,"location":"rack-1","primaryControllerName":"primary","primaryIpAddress":"2001:0db8:0:0:0:0:0:1"}`), &row); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "mismatch":
				value := "192.0.2.1"
				row.PrimaryIP = &value
			case "missing-field":
				row.LEDBrightness = nil
			case "duplicate-radio-type", "wrong-radio-type":
				kind, slot, slot2, name := 2, 0, 1, "antenna"
				if mode == "wrong-radio-type" {
					kind = 3
				}
				row.Radios = []accessPointRadioObservation{{Type: &kind, Slot: &slot, AntennaName: &name}, {Type: &kind, Slot: &slot2, AntennaName: &name}}
			case "no-rename":
				row.Name = "unmanaged-name"
			case "bad-native-identity":
				row.EthernetMAC = "invalid"
			}
			comparisons, err := compareAccessPointSettings(want, row)
			if mode == "bad-native-identity" {
				if err == nil {
					t.Fatal("invalid native row accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if accessPointSettingsMatch(comparisons) != (mode == "matched" || mode == "rename") {
				t.Fatal("comparison invented convergence or ignored selected fields", mode, comparisons)
			}
			if mode == "duplicate-radio-type" {
				for _, comparison := range comparisons {
					if comparison.path == "/radioConfigurations/2/antennaPatternName" && comparison.state != apSettingUnavailable {
						t.Fatal("ambiguous radio slots accepted")
					}
				}
			}
		})
	}
}
