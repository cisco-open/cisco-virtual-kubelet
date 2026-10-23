// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
)

//go:embed definitions/topology-custom-control-schema.json
var topologyCustomControlSchema []byte

func compileCustomControlPolicies(p FeatureProfile, family, key, managerVersion string) ([]Resource, error) {
	if len(p.CustomPolicies) > 0 && family != "topology" {
		return nil, errors.New("custom control policies require topology profile")
	}
	out := make([]Resource, 0, len(p.CustomPolicies))
	for _, input := range p.CustomPolicies {
		name, ok := input["name"].(string)
		if !ok || name == "" || len(name) > 128 {
			return nil, errors.New("custom control policy requires name")
		}
		data, sites, err := customControlData(input, managerVersion)
		if err != nil {
			return nil, fmt.Errorf("custom control %s: %w", name, err)
		}
		allSites := append(append([]string{}, sites["inbound"]...), sites["outbound"]...)
		out = append(out, Resource{Key: key + "/custom_policies/" + name, Name: name, Section: "feature_profiles", Kind: "policy-parcel", Type: "topology_custom_control", Collection: "/dataservice/v1/feature-profile/sdwan/topology", ParentName: p.Name, ParcelPath: "custom-control", HierarchySites: allSites, CustomControlSites: sites, Body: map[string]any{"name": name, "description": "", "data": data}})
	}
	return out, nil
}

func customControlData(input map[string]any, managerVersion string) (map[string]any, map[string][]string, error) {
	allowed := map[string]bool{"name": true, "default_action": true, "sequences": true, "inbound_sites": true, "outbound_sites": true}
	for field := range input {
		if !allowed[field] {
			return nil, nil, fmt.Errorf("unsupported custom control field %s", field)
		}
	}
	action := "reject"
	if input["default_action"] != nil {
		var ok bool
		action, ok = input["default_action"].(string)
		if !ok {
			return nil, nil, errors.New("custom control default_action must be string")
		}
	}
	modern, err := managementInterfaceVersion(managerVersion)
	if err != nil {
		return nil, nil, err
	}
	sites := map[string][]string{}
	target := map[string]any{"level": map[string]any{"optionType": "global", "value": "SITE"}}
	for _, direction := range []string{"inbound", "outbound"} {
		selected, err := meshStrings(input[direction+"_sites"], false)
		if err != nil {
			return nil, nil, err
		}
		sites[direction] = selected
		values := []string{}
		for _, site := range selected {
			if modern {
				values = append(values, parcelPlaceholder(site))
			} else {
				values = append(values, site)
			}
		}
		field := direction + "Sites"
		if modern {
			field = direction + "HierarchyUuid"
		}
		target[field] = map[string]any{"optionType": "global", "value": values}
	}
	data := map[string]any{"defaultAction": map[string]any{"optionType": "global", "value": action}, "target": target}
	rows, err := vpnRows(input, "sequences")
	if err != nil {
		return nil, nil, err
	}
	sequences := []any{}
	for _, row := range rows {
		entry, ok := row.(map[string]any)
		if !ok {
			return nil, nil, errors.New("custom control sequence must be object")
		}
		sequence, err := customControlSequence(entry)
		if err != nil {
			return nil, nil, err
		}
		sequences = append(sequences, sequence)
	}
	data["sequences"] = sequences
	var schema map[string]any
	var normalized any
	encoded, _ := json.Marshal(data)
	if json.Unmarshal(topologyCustomControlSchema, &schema) != nil || json.Unmarshal(encoded, &normalized) != nil {
		return nil, nil, errors.New("invalid custom control schema")
	}
	if !validScalarValue(normalized, schema) {
		for key, value := range normalized.(map[string]any) {
			if !validScalarValue(value, schema["properties"].(map[string]any)[key].(map[string]any)) {
				return nil, nil, fmt.Errorf("custom control field %s violates provider schema", key)
			}
		}
		return nil, nil, errors.New("custom control intent violates provider schema")
	}
	return data, sites, nil
}

func (c *Client) resolveCustomControlSites(ctx context.Context, r Resource) error {
	if r.Type != "topology_custom_control" || len(r.HierarchySites) == 0 {
		return nil
	}
	target := r.Body["data"].(map[string]any)["target"].(map[string]any)
	if target["inboundHierarchyUuid"] == nil {
		return nil
	}
	nodes, err := c.hierarchy(ctx)
	if err != nil {
		return err
	}
	for _, direction := range []string{"inbound", "outbound"} {
		ids := []string{}
		for _, site := range r.CustomControlSites[direction] {
			id := ""
			for _, node := range nodes {
				if node["name"] == site {
					data, _ := node["data"].(map[string]any)
					if data["label"] != "SITE" || id != "" {
						return ErrResponse
					}
					id, _ = node["id"].(string)
				}
			}
			if id == "" {
				return ErrDependencyPending
			}
			if !validID.MatchString(id) {
				return ErrResponse
			}
			ids = append(ids, id)
		}
		target[direction+"HierarchyUuid"].(map[string]any)["value"] = ids
	}
	return nil
}

func customControlSequence(input map[string]any) (map[string]any, error) {
	allowed := map[string]bool{"sequence_id": true, "sequence_name": true, "base_action": true, "type": true, "protocol": true}
	for field := range input {
		if !allowed[field] {
			return nil, fmt.Errorf("unsupported custom control sequence field %s", field)
		}
	}
	id, ok := input["sequence_id"].(float64)
	if !ok || id < 1 || id > 6553 || float64(int(id)) != id {
		return nil, errors.New("custom control sequence_id must be integer 1..6553")
	}
	name := fmt.Sprintf("Rule%d", int(id))
	if input["sequence_name"] != nil {
		name, ok = input["sequence_name"].(string)
		if !ok || name == "" {
			return nil, errors.New("custom control sequence_name must be nonempty string")
		}
	}
	action := "reject"
	if input["base_action"] != nil {
		action, ok = input["base_action"].(string)
		if !ok {
			return nil, errors.New("custom control base_action must be string")
		}
	}
	sequenceType := "route"
	if input["type"] != nil {
		sequenceType, ok = input["type"].(string)
		if !ok {
			return nil, errors.New("custom control type must be string")
		}
	}
	protocol := "ipv4"
	if input["protocol"] != nil {
		protocol, ok = input["protocol"].(string)
		if !ok {
			return nil, errors.New("custom control protocol must be string")
		}
	}
	if protocol == "both" {
		protocol = "all"
	}
	return map[string]any{"sequenceId": map[string]any{"optionType": "global", "value": id * 10}, "sequenceName": map[string]any{"optionType": "global", "value": name}, "baseAction": map[string]any{"optionType": "global", "value": action}, "sequenceType": map[string]any{"optionType": "global", "value": sequenceType}, "sequenceIpType": map[string]any{"optionType": "global", "value": protocol}, "match": map[string]any{"entries": []any{}}, "actions": []any{}}, nil
}
