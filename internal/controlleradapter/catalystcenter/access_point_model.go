// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

var errAccessPointModel = errors.New("invalid access point configuration intent")

type accessPointProfile struct {
	name    string
	payload map[string]any
}
type apSettingField struct {
	api, gate, kind string
	min, max        float64
	choices         map[string]any
}

// These are the pinned module's intent fields. API configure flags are derived
// from presence rather than accepted as user input. Defaults are resolved by
// the model exporter; explicit null leaves a field unselected.
var apProfileFields = map[string]apSettingField{
	"admin_status":                 {"adminStatus", "configureAdminStatus", "bool", 0, 0, nil},
	"ap_mode":                      {"apMode", "configureApMode", "enum", 0, 0, map[string]any{"LOCAL": 0, "MONITOR": 1, "SNIFFER": 4, "BRIDGE": 5}},
	"failover_priority":            {"failoverPriority", "configureFailoverPriority", "enum", 0, 0, map[string]any{"LOW": 1, "MEDIUM": 2, "HIGH": 3, "CRITICAL": 4}},
	"led_status":                   {"ledStatus", "configureLedStatus", "bool", 0, 0, nil},
	"led_brightness_level":         {"ledBrightnessLevel", "configureLedBrightnessLevel", "integer", 1, 8, nil},
	"location":                     {"location", "configureLocation", "text", 0, 0, nil},
	"is_assigned_site_as_location": {"isAssignedSiteAsLocation", "", "bool", 0, 0, nil},
	"primary_controller_name":      {"primaryControllerName", "configureHAController", "text", 0, 0, nil},
	"primary_ip_address":           {"primaryIpAddress", "", "ip", 0, 0, nil},
	"secondary_controller_name":    {"secondaryControllerName", "", "text", 0, 0, nil},
	"secondary_ip_address":         {"secondaryIpAddress", "", "ip", 0, 0, nil},
	"tertiary_controller_name":     {"tertiaryControllerName", "", "text", 0, 0, nil},
	"tertiary_ip_address":          {"tertiaryIpAddress", "", "ip", 0, 0, nil},
}
var apRadioFields = map[string]apSettingField{
	"admin_status":            {"adminStatus", "configureAdminStatus", "bool", 0, 0, nil},
	"role":                    {"radioRoleAssignment", "configureRadioRoleAssignment", "enum", 0, 0, map[string]any{"AUTO": "AUTO", "SERVING": "SERVING", "MONITOR": "MONITOR"}},
	"antenna_pattern_name":    {"antennaPatternName", "configureAntennaPatternName", "text", 0, 0, nil},
	"antenna_gain":            {"antennaGain", "", "integer", 0, 20, nil},
	"antenna_cable_name":      {"antennaCableName", "configureAntennaCable", "text", 0, 0, nil},
	"cable_loss":              {"cableLoss", "", "number", 0, 0, nil},
	"channel_assignment_mode": {"channelAssignmentMode", "configureChannel", "enum", 0, 0, map[string]any{"GLOBAL": 1, "CUSTOM": 2}},
	"channel_number":          {"channelNumber", "configureChannel", "integer", 0, 0, nil},
	"channel_width":           {"channelWidth", "configureChannelWidth", "enum", 0, 0, map[string]any{"20": 3, "40": 4, "80": 5, "160": 6, "320": 7}},
	"power_assignment_mode":   {"powerAssignmentMode", "configurePower", "enum", 0, 0, map[string]any{"GLOBAL": 1, "CUSTOM": 2}},
	"power_level":             {"powerLevel", "configurePower", "integer", 1, 8, nil},
}

func mapAPSettings(model map[string]any, fields map[string]apSettingField) (map[string]any, error) {
	out := map[string]any{}
	for _, field := range fields {
		if field.gate != "" {
			out[field.gate] = false
		}
	}
	for key, value := range model {
		field, ok := fields[key]
		if !ok {
			return nil, errAccessPointModel
		}
		if value == nil {
			continue
		}
		var mapped any
		switch field.kind {
		case "bool":
			b, ok := value.(bool)
			if !ok {
				return nil, errAccessPointModel
			}
			mapped = b
		case "text":
			s, err := plannedAPText(value)
			if err != nil {
				return nil, errAccessPointModel
			}
			mapped = s
		case "ip":
			s, ok := value.(string)
			if !ok {
				return nil, errAccessPointModel
			}
			ip, err := netip.ParseAddr(s)
			if err != nil || ip.Zone() != "" {
				return nil, errAccessPointModel
			}
			mapped = map[string]any{"address": ip.String()}
		case "enum":
			mapped, ok = field.choices[fmt.Sprint(value)]
			if !ok {
				return nil, errAccessPointModel
			}
		case "integer", "number":
			n, err := plannedAPNumber(value, field.kind == "integer")
			if err != nil || field.max > 0 && (n < field.min || n > field.max) {
				return nil, errAccessPointModel
			}
			mapped = n
		default:
			return nil, errAccessPointModel
		}
		out[field.api] = mapped
		if field.gate != "" {
			out[field.gate] = true
		}
	}
	return out, nil
}

func parseAccessPointProfiles(value any) ([]accessPointProfile, error) {
	list, ok := value.([]any)
	if !ok || len(list) > 1000 {
		return nil, errAccessPointModel
	}
	out := []accessPointProfile{}
	seen := map[string]bool{}
	for _, entry := range list {
		model, ok := entry.(map[string]any)
		if !ok {
			return nil, errAccessPointModel
		}
		name, err := plannedAPText(model["name"])
		if err != nil || seen[name] || name == "(hostname only)" {
			return nil, errAccessPointModel
		}
		seen[name] = true
		settings := map[string]any{}
		for k, v := range model {
			if k != "name" && k != "radios" {
				settings[k] = v
			}
		}
		payload, err := mapAPSettings(settings, apProfileFields)
		if err != nil {
			return nil, err
		}
		// The native HA gate is derived only from primary_controller_name.
		// Reject selected dependent settings that would otherwise be accepted
		// but silently ignored, rather than publishing a false convergence.
		if payload["configureHAController"] != true {
			for _, key := range []string{"primaryIpAddress", "secondaryControllerName", "secondaryIpAddress", "tertiaryControllerName", "tertiaryIpAddress"} {
				if payload[key] != nil {
					return nil, errAccessPointModel
				}
			}
		}
		if payload["isAssignedSiteAsLocation"] == true {
			if payload["location"] != nil {
				return nil, errAccessPointModel
			}
			payload["configureLocation"] = true
		}
		if value, present := model["radios"]; present && value != nil {
			radios, ok := value.([]any)
			if !ok || len(radios) > 4 {
				return nil, errAccessPointModel
			}
			mapped := []any{}
			bands := map[any]bool{}
			for _, entry := range radios {
				radio, ok := entry.(map[string]any)
				if !ok {
					return nil, errAccessPointModel
				}
				parsed, err := parseAccessPointRadio(radio)
				if err != nil {
					return nil, err
				}
				if bands[parsed["radioType"]] {
					return nil, errAccessPointModel
				}
				bands[parsed["radioType"]] = true
				mapped = append(mapped, parsed)
			}
			if len(mapped) > 0 {
				payload["radioConfigurations"] = mapped
			}
		}
		out = append(out, accessPointProfile{name: name, payload: payload})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func parseAccessPointRadio(model map[string]any) (map[string]any, error) {
	band := fmt.Sprint(model["band"])
	types := map[string]int{"2.4": 1, "5": 2, "XOR": 3, "6": 6}
	native := map[string]string{"2.4": "RADIO24", "5": "RADIO5", "XOR": "RADIO5", "6": "RADIO6"}
	radioType, ok := types[band]
	if !ok {
		return nil, errAccessPointModel
	}
	fields := map[string]any{}
	for k, v := range model {
		if k != "band" {
			fields[k] = v
		}
	}
	payload, err := mapAPSettings(fields, apRadioFields)
	if err != nil {
		return nil, err
	}
	payload["radioType"] = radioType
	role := payload["radioRoleAssignment"]
	if role != "AUTO" && role != "MONITOR" {
		payload["radioBand"] = native[band]
	}
	if (payload["configureChannel"] == true || payload["configureChannelWidth"] == true || payload["configurePower"] == true) && role != "SERVING" {
		return nil, errAccessPointModel
	}
	// Match pinned ap_configuration_validation: explicit RF values are owned
	// by the request only in CUSTOM mode; GLOBAL leaves them under RRM control.
	if (payload["channelNumber"] != nil || payload["channelWidth"] != nil) && payload["channelAssignmentMode"] != 2 || payload["powerLevel"] != nil && payload["powerAssignmentMode"] != 2 {
		return nil, errAccessPointModel
	}
	if band == "2.4" && payload["channelWidth"] != nil {
		return nil, errAccessPointModel
	}
	for value, name := range map[string]string{"antennaGain": "antennaPatternName", "cableLoss": "antennaCableName"} {
		if payload[value] != nil {
			s, _ := payload[name].(string)
			if !strings.EqualFold(s, "other") {
				return nil, errAccessPointModel
			}
		}
	}
	return payload, nil
}
