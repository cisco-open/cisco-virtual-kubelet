// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

//go:embed definitions/embedded-security-ngfw-schema.json
var embeddedSecurityNGFWSchema []byte

func compileNGFWPolicies(p FeatureProfile, family, key string) ([]Resource, error) {
	if len(p.NGFWPolicies) > 0 && family != "embedded-security" {
		return nil, errors.New("NGFW policies require embedded-security profile")
	}
	out := make([]Resource, 0, len(p.NGFWPolicies))
	for _, input := range p.NGFWPolicies {
		name, ok := input["name"].(string)
		if !ok || name == "" || len(name) > 128 {
			return nil, errors.New("NGFW policy requires name")
		}
		for field := range input {
			switch field {
			case "name", "default_action", "sequences":
			default:
				return nil, fmt.Errorf("unsupported NGFW policy field %s", field)
			}
		}
		action, ok := input["default_action"].(string)
		if !ok {
			return nil, errors.New("NGFW policy requires default_action")
		}
		rows, err := vpnRows(input, "sequences")
		if err != nil {
			return nil, err
		}
		sequences := []any{}
		seen := map[int]bool{}
		for _, raw := range rows {
			row, ok := raw.(map[string]any)
			if !ok {
				return nil, errors.New("NGFW sequence must be object")
			}
			for field := range row {
				switch field {
				case "sequence_id", "sequence_name", "base_action", "source_data_ipv4_prefixes", "destination_data_ipv4_prefixes":
				default:
					return nil, fmt.Errorf("unsupported NGFW sequence field %s", field)
				}
			}
			id, ok := row["sequence_id"].(float64)
			if !ok || id < 0 || id > 65535 || id != float64(int(id)) || seen[int(id)] {
				return nil, errors.New("NGFW sequence_id must be unique integer 0..65535")
			}
			seen[int(id)] = true
			sequenceName, ok := row["sequence_name"].(string)
			if !ok || sequenceName == "" {
				return nil, errors.New("NGFW sequence requires sequence_name")
			}
			base, ok := row["base_action"].(string)
			if !ok {
				return nil, errors.New("NGFW sequence requires base_action")
			}
			matches := []any{}
			for field, api := range map[string]string{"source_data_ipv4_prefixes": "sourceIp", "destination_data_ipv4_prefixes": "destinationIp"} {
				if raw := row[field]; raw != nil {
					values, err := meshStrings(raw, false)
					if err != nil || len(values) == 0 {
						return nil, fmt.Errorf("invalid NGFW %s", field)
					}
					matches = append(matches, map[string]any{api: map[string]any{"ipv4Value": map[string]any{"optionType": "global", "value": values}}})
				}
			}
			sequences = append(sequences, map[string]any{"sequenceId": map[string]any{"optionType": "global", "value": strconv.Itoa(int(id))}, "sequenceName": map[string]any{"optionType": "global", "value": sequenceName}, "baseAction": map[string]any{"optionType": "global", "value": base}, "sequenceType": map[string]any{"optionType": "global", "value": "ngfirewall"}, "disableSequence": map[string]any{"optionType": "global", "value": false}, "match": map[string]any{"entries": matches}, "actions": []any{}})
		}
		data := map[string]any{"defaultActionType": map[string]any{"optionType": "global", "value": action}, "sequences": sequences}
		var schema map[string]any
		var normalized any
		encoded, _ := json.Marshal(data)
		if json.Unmarshal(embeddedSecurityNGFWSchema, &schema) != nil || json.Unmarshal(encoded, &normalized) != nil || !validScalarValue(normalized, schema) {
			return nil, errors.New("NGFW policy violates provider schema")
		}
		out = append(out, Resource{Key: key + "/policies/" + name, Name: name, Section: "feature_profiles", Kind: "policy-parcel", Type: "embedded_security_ngfw", Collection: "/dataservice/v1/feature-profile/sdwan/embedded-security", ParentName: p.Name, ParcelPath: "unified/ngfirewall", Body: map[string]any{"name": name, "description": "NG Firewall policy", "data": data}})
	}
	return out, nil
}
