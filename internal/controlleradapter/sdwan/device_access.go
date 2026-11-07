// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
)

// Generated from the pinned provider's system device-access request schemas.
//
//go:embed definitions/device-access-schemas.json
var deviceAccessSchemas []byte

func deviceAccessData(input map[string]any, ip string) (ParcelMetadata, map[string]any, error) {
	meta := ParcelMetadata{}
	for k := range input {
		switch k {
		case "name", "description", "default_action", "sequences":
		default:
			return meta, nil, fmt.Errorf("unsupported device access field %s", k)
		}
	}
	if v := input["name"]; v != nil {
		name, ok := v.(string)
		if !ok || name == "" || len(name) > 128 {
			return meta, nil, errors.New("invalid device access name")
		}
		meta.Name = &name
	}
	if v := input["description"]; v != nil {
		description, ok := v.(string)
		if !ok {
			return meta, nil, errors.New("invalid device access description")
		}
		meta.Description = description
	}
	defaultAction, ok := input["default_action"].(string)
	if !ok || (defaultAction != "accept" && defaultAction != "drop") {
		return meta, nil, errors.New("device access default_action must be accept or drop")
	}
	data := map[string]any{"defaultAction": option(&defaultAction, nil, "drop", false), "sequences": []any{}}
	if defaultAction == "drop" {
		data["defaultAction"] = map[string]any{"optionType": "default", "value": "drop"}
	}
	if v := input["sequences"]; v != nil {
		sequences, ok := v.([]any)
		if !ok {
			return meta, nil, errors.New("device access sequences must be an array")
		}
		out := make([]any, 0, len(sequences))
		for _, entry := range sequences {
			sequence, ok := entry.(map[string]any)
			if !ok {
				return meta, nil, errors.New("device access sequence must be an object")
			}
			for k := range sequence {
				switch k {
				case "id", "name", "base_action", "match_entries":
				default:
					return meta, nil, fmt.Errorf("unsupported device access sequence field %s", k)
				}
			}
			id, ok := sequence["id"].(float64)
			if !ok {
				return meta, nil, errors.New("device access sequence id is required")
			}
			name := "acl"
			if v := sequence["name"]; v != nil {
				var ok bool
				name, ok = v.(string)
				if !ok {
					return meta, nil, errors.New("invalid device access sequence name")
				}
			}
			action, ok := sequence["base_action"].(string)
			if !ok || (action != "accept" && action != "drop") {
				return meta, nil, errors.New("invalid device access base_action")
			}
			matches, ok := sequence["match_entries"].(map[string]any)
			if !ok {
				return meta, nil, errors.New("device access match_entries are required")
			}
			for _, direction := range []string{"destination", "source"} {
				count := 0
				for _, suffix := range []string{"data_prefixes", "data_prefixes_variable", "data_prefix_list"} {
					if matches[direction+"_"+suffix] != nil {
						count++
					}
				}
				if count > 1 {
					return meta, nil, errors.New("device access prefix inputs conflict")
				}
			}
			match := map[string]any{}
			for k, v := range matches {
				switch k {
				case "destination_port":
					match["destinationPort"] = map[string]any{"optionType": "global", "value": v}
				case "destination_data_prefixes", "source_data_prefixes", "source_ports":
					path := map[string]string{"destination_data_prefixes": "destinationDataPrefix.destinationIpPrefixList", "source_data_prefixes": "sourceDataPrefix.sourceIpPrefixList", "source_ports": "sourcePorts"}[k]
					setScalarPath(match, path, map[string]any{"optionType": "global", "value": v})
				case "destination_data_prefixes_variable", "source_data_prefixes_variable":
					if ip != "ipv4" {
						return meta, nil, errors.New("IPv6 device access does not support prefix variables")
					}
					prefix, ok := v.(string)
					if !ok || !validParcelVariable.MatchString(prefix) {
						return meta, nil, errors.New("invalid device access prefix variable")
					}
					path := "destinationDataPrefix.destinationIpPrefixList"
					if k == "source_data_prefixes_variable" {
						path = "sourceDataPrefix.sourceIpPrefixList"
					}
					setScalarPath(match, path, map[string]any{"optionType": "variable", "value": "{{" + prefix + "}}"})
				case "destination_data_prefix_list", "source_data_prefix_list":
					name, ok := v.(string)
					if !ok || name == "" || len(name) > 128 {
						return meta, nil, errors.New("invalid device access prefix-list reference")
					}
					path := "destinationDataPrefix.destinationDataPrefixList.refId"
					if k == "source_data_prefix_list" {
						path = "sourceDataPrefix.sourceDataPrefixList.refId"
					}
					setScalarPath(match, path, map[string]any{"optionType": "global", "value": parcelPlaceholder(name)})
				default:
					return meta, nil, fmt.Errorf("unsupported device access match field %s", k)
				}
			}
			for _, direction := range []string{"destination", "source"} {
				if node, ok := match[direction+"DataPrefix"].(map[string]any); ok && len(node) > 1 {
					return meta, nil, errors.New("device access prefix list and inline prefixes conflict")
				}
			}
			out = append(out, map[string]any{"sequenceId": map[string]any{"optionType": "global", "value": id}, "sequenceName": map[string]any{"optionType": "global", "value": name}, "baseAction": map[string]any{"optionType": "global", "value": action}, "matchEntries": match})
		}
		data["sequences"] = out
	}
	var schemas map[string]map[string]any
	if json.Unmarshal(deviceAccessSchemas, &schemas) != nil || !validScalarValue(data, schemas[ip]) {
		return meta, nil, errors.New("device access intent violates provider schema")
	}
	return meta, data, nil
}

func deviceAccessReferences(input map[string]any, ip string) []ParcelReference {
	refs := []ParcelReference{}
	sequences, _ := input["sequences"].([]any)
	for i, entry := range sequences {
		sequence, _ := entry.(map[string]any)
		matches, _ := sequence["match_entries"].(map[string]any)
		for _, direction := range []string{"destination", "source"} {
			name, ok := matches[direction+"_data_prefix_list"].(string)
			if !ok {
				continue
			}
			path := "data-prefix"
			if ip == "ipv6" {
				path = "data-ipv6-prefix"
			}
			refs = append(refs, ParcelReference{Kind: "policy_object_data_" + ip + "_prefix_list", Name: name, ParcelPath: path,
				Collection: "/dataservice/v1/feature-profile/sdwan/policy-object", ParentName: "$policy_objects",
				BodyPath: []string{"data", "sequences", fmt.Sprint(i), "matchEntries", direction + "DataPrefix", direction + "DataPrefixList", "refId", "value"}})
		}
	}
	return refs
}
