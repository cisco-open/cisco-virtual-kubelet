// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
)

//go:embed definitions/application-priority-traffic-schema.json
var applicationPriorityTrafficSchema []byte

func compileTrafficPolicies(p FeatureProfile, family, key string) ([]Resource, error) {
	if len(p.TrafficPolicies) > 0 && family != "application-priority" {
		return nil, errors.New("traffic policies require application-priority profile")
	}
	out := make([]Resource, 0, len(p.TrafficPolicies))
	for _, input := range p.TrafficPolicies {
		name, ok := input["name"].(string)
		if !ok || name == "" || len(name) > 128 {
			return nil, errors.New("traffic policy requires name")
		}
		for field := range input {
			switch field {
			case "name", "default_action", "vpns", "direction", "sequences":
			default:
				return nil, fmt.Errorf("unsupported traffic policy field %s", field)
			}
		}
		action, ok := input["default_action"].(string)
		if !ok {
			return nil, errors.New("traffic policy requires default_action")
		}
		direction, ok := input["direction"].(string)
		if !ok {
			return nil, errors.New("traffic policy requires direction")
		}
		vpns, err := meshStrings(input["vpns"], false)
		if err != nil || len(vpns) == 0 {
			return nil, errors.New("traffic policy requires vpns")
		}
		data := map[string]any{"dataDefaultAction": map[string]any{"optionType": "global", "value": action}, "target": map[string]any{"vpn": map[string]any{"optionType": "global", "value": vpns}, "direction": map[string]any{"optionType": "global", "value": direction}}}
		rows, err := vpnRows(input, "sequences")
		if err != nil {
			return nil, err
		}
		sequences := []any{}
		ids := map[int]bool{}
		for _, raw := range rows {
			row, ok := raw.(map[string]any)
			if !ok {
				return nil, errors.New("traffic sequence must be object")
			}
			for field := range row {
				switch field {
				case "sequence_id", "sequence_name", "base_action", "protocol":
				default:
					return nil, fmt.Errorf("unsupported traffic sequence field %s", field)
				}
			}
			id, ok := row["sequence_id"].(float64)
			if !ok || id < 1 || id > 6553 || id != float64(int(id)) || ids[int(id)] {
				return nil, errors.New("traffic sequence_id must be unique integer 1..6553")
			}
			ids[int(id)] = true
			sequenceName, ok := row["sequence_name"].(string)
			if !ok || sequenceName == "" {
				return nil, errors.New("traffic sequence requires sequence_name")
			}
			base, ok := row["base_action"].(string)
			if !ok {
				return nil, errors.New("traffic sequence requires base_action")
			}
			protocol := "ipv4"
			if raw := row["protocol"]; raw != nil {
				protocol, ok = raw.(string)
				if !ok {
					return nil, errors.New("traffic protocol must be string")
				}
			}
			if protocol == "both" {
				protocol = "all"
			}
			sequences = append(sequences, map[string]any{"sequenceId": map[string]any{"optionType": "global", "value": (id-1)*10 + 1}, "sequenceName": map[string]any{"optionType": "global", "value": sequenceName}, "baseAction": map[string]any{"optionType": "global", "value": base}, "sequenceIpType": map[string]any{"optionType": "global", "value": protocol}, "match": map[string]any{"entries": []any{}}, "actions": []any{}})
		}
		data["sequences"] = sequences
		var schema map[string]any
		var normalized any
		encoded, _ := json.Marshal(data)
		if json.Unmarshal(applicationPriorityTrafficSchema, &schema) != nil || json.Unmarshal(encoded, &normalized) != nil {
			return nil, errors.New("invalid traffic policy schema")
		}
		if !validScalarValue(normalized, schema) {
			for field, value := range normalized.(map[string]any) {
				if !validScalarValue(value, schema["properties"].(map[string]any)[field].(map[string]any)) {
					if field == "sequences" {
						itemSchema := schema["properties"].(map[string]any)[field].(map[string]any)["items"].(map[string]any)
						for itemField, itemValue := range value.([]any)[0].(map[string]any) {
							if !validScalarValue(itemValue, itemSchema["properties"].(map[string]any)[itemField].(map[string]any)) {
								return nil, fmt.Errorf("traffic sequence field %s violates provider schema", itemField)
							}
						}
					}
					return nil, fmt.Errorf("traffic policy field %s violates provider schema", field)
				}
			}
			return nil, errors.New("traffic policy violates provider schema")
		}
		out = append(out, Resource{Key: key + "/traffic_policies/" + name, Name: name, Section: "feature_profiles", Kind: "policy-parcel", Type: "application_priority_traffic_policy", Collection: "/dataservice/v1/feature-profile/sdwan/application-priority", ParentName: p.Name, ParcelPath: "traffic-policy", Body: map[string]any{"name": name, "description": "", "data": data}})
	}
	return out, nil
}
