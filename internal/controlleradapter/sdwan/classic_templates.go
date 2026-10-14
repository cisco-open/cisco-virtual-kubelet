// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
)

//go:embed definitions/classic-templates.json
var classicDefinitions []byte

type classicField struct {
	ObjectType     string `json:"object_type"`
	Path, Variable string
	Schema         map[string]any
	OmitNull       bool `json:"omit_null"`
	BooleanString  bool `json:"boolean_string"`
}
type classicDefinition struct {
	Defaults    map[string]any
	Type        string
	Fields      map[string]classicField
	Required    []string
	DeviceTypes []string `json:"device_types"`
}

func compileClassicTemplates(collection string, templates []map[string]any, override []string) ([]Resource, error) {
	var registry struct{ Templates map[string]classicDefinition }
	if json.Unmarshal(classicDefinitions, &registry) != nil {
		return nil, ErrResponse
	}
	definition, ok := registry.Templates[collection]
	if !ok {
		return nil, errors.New("unsupported classic template collection")
	}
	out := []Resource{}
	seen := map[string]bool{}
	for _, input := range templates {
		name, ok := input["name"].(string)
		if !ok || name == "" || len(name) > 128 || seen[name] {
			return nil, errors.New("classic templates require unique names")
		}
		seen[name] = true
		description, ok := input["description"].(string)
		if !ok {
			return nil, errors.New("classic template description is required")
		}
		devices := definition.DeviceTypes
		if override != nil {
			devices = override
		}
		if raw, exists := input["device_types"]; exists {
			list, ok := raw.([]any)
			if !ok {
				return nil, errors.New("device_types must be an array")
			}
			devices = []string{}
			for _, value := range list {
				s, ok := value.(string)
				if !ok || s == "" {
					return nil, errors.New("invalid device type")
				}
				devices = append(devices, s)
			}
		}
		if len(devices) == 0 {
			return nil, errors.New("classic template requires device_types")
		}
		mapped := []string{}
		seenDevice := map[string]bool{}
		for _, d := range devices {
			if d == "" {
				return nil, errors.New("invalid device type")
			}
			if !seenDevice[d] {
				mapped = append(mapped, "vedge-"+d)
				seenDevice[d] = true
			}
		}
		sort.Strings(mapped)
		for _, field := range definition.Required {
			if input[field] == nil {
				return nil, fmt.Errorf("%s is required", field)
			}
		}
		allowed := map[string]bool{"name": true, "description": true, "device_types": true}
		data := map[string]any{}
		baseline, err := json.Marshal(definition.Defaults)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(baseline, &data); err != nil {
			return nil, err
		}
		for public, field := range definition.Fields {
			allowed[public] = true
			var variable any
			if field.Variable != "" {
				allowed[field.Variable] = true
				variable = input[field.Variable]
			}
			leaf := map[string]any{"vipObjectType": field.ObjectType}
			if variable != nil {
				v, ok := variable.(string)
				if !ok || len(v) == 0 || len(v) > 128 {
					return nil, fmt.Errorf("invalid %s variable", public)
				}
				leaf["vipType"] = "variableName"
				leaf["vipVariableName"] = v
			} else if value := input[public]; value != nil {
				if !validScalarValue(value, field.Schema) {
					return nil, fmt.Errorf("invalid %s value", public)
				}
				if field.BooleanString {
					value = strconv.FormatBool(value.(bool))
				}
				leaf["vipType"] = "constant"
				leaf["vipValue"] = value
			} else if field.OmitNull {
				continue
			} else {
				leaf["vipType"] = "ignore"
			}
			setScalarPath(data, field.Path, leaf)
		}
		for field := range input {
			if !allowed[field] {
				return nil, fmt.Errorf("unsupported classic field %s", field)
			}
		}
		out = append(out, Resource{Key: "edge_feature_templates/" + collection + "/" + name, Name: name, Section: "edge_feature_templates", Type: definition.Type, Body: map[string]any{"deviceType": mapped, "factoryDefault": false, "templateDescription": description, "templateMinVersion": "15.0.0", "templateName": name, "templateType": definition.Type, "templateDefinition": data}})
	}
	return out, nil
}

func normalizeVIP(value any) (any, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return value, nil
	}
	if kind, exists := object["vipObjectType"]; exists {
		if kind == "tree" {
			entries, ok := object["vipValue"].([]any)
			keys, keysOK := object["vipPrimaryKey"].([]any)
			if object["vipType"] != "ignore" || !ok || len(entries) != 0 || !keysOK {
				return nil, ErrResponse
			}
			return map[string]any{"vipObjectType": kind, "vipType": "ignore", "vipPrimaryKey": keys, "vipValue": entries}, nil
		}
		if kind != "object" && kind != "node-only" {
			return nil, ErrResponse
		}
		out := map[string]any{"vipObjectType": kind, "vipType": object["vipType"]}
		switch object["vipType"] {
		case "constant":
			v, exists := object["vipValue"]
			if !exists {
				return nil, ErrResponse
			}
			out["vipValue"] = v
		case "variableName":
			v, ok := object["vipVariableName"].(string)
			if !ok {
				return nil, ErrResponse
			}
			out["vipVariableName"] = v
		case "ignore":
		default:
			return nil, ErrResponse
		}
		return out, nil
	}
	out := map[string]any{}
	for k, v := range object {
		normalized, err := normalizeVIP(v)
		if err != nil {
			return nil, err
		}
		out[k] = normalized
	}
	return out, nil
}
