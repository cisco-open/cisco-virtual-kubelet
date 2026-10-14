// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

//go:embed definitions/classic-templates.json
var classicDefinitions []byte

type classicField struct {
	Present        string
	Values         map[string]string
	BooleanChoice  string         `json:"boolean_choice"`
	InputDefault   any            `json:"input_default"`
	InputSchema    map[string]any `json:"input_schema"`
	DecimalString  bool           `json:"decimal_string"`
	WrapList       bool           `json:"wrap_list"`
	Ranges         string
	Literal        any
	Derived        bool
	When           string
	DerivedValue   any `json:"derived_value"`
	Set            bool
	ObjectType     string `json:"object_type"`
	Path, Variable string
	Schema         map[string]any
	OmitNull       bool `json:"omit_null"`
	BooleanString  bool `json:"boolean_string"`
}
type classicTree struct {
	Primitive   bool
	Path        string
	PrimaryKeys []string `json:"primary_keys"`
	Definition  classicDefinition
}
type classicDefinition struct {
	Wrapper             *classicTree
	AlwaysPriorityOrder bool `json:"always_priority_order"`
	Trees               map[string]classicTree
	Optional            string
	PriorityOrder       []string `json:"priority_order"`
	Defaults            map[string]any
	Type                string
	Fields              map[string]classicField
	Required            []string
	DeviceTypes         []string `json:"device_types"`
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
		data, err := classicData(definition, input, true)
		if err != nil {
			return nil, err
		}
		if wrapper := definition.Wrapper; wrapper != nil {
			wrapped := map[string]any{}
			setScalarPath(wrapped, wrapper.Path, map[string]any{"vipObjectType": "tree", "vipType": "constant", "vipPrimaryKey": wrapper.PrimaryKeys, "vipValue": []any{data}})
			data = wrapped
		}
		out = append(out, Resource{Key: "edge_feature_templates/" + collection + "/" + name, Name: name, Section: "edge_feature_templates", Type: definition.Type, Body: map[string]any{"deviceType": mapped, "factoryDefault": false, "templateDescription": description, "templateMinVersion": "15.0.0", "templateName": name, "templateType": definition.Type, "templateDefinition": data}})
	}
	return out, nil
}

func classicData(definition classicDefinition, input map[string]any, metadata bool) (map[string]any, error) {
	for _, field := range definition.Required {
		if classicInput(input, field) == nil {
			return nil, fmt.Errorf("%s is required", field)
		}
	}
	allowed := map[string]bool{}
	if metadata {
		allowed["name"] = true
		allowed["description"] = true
		allowed["device_types"] = true
	}
	data := map[string]any{}
	baseline, err := json.Marshal(definition.Defaults)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(baseline, &data); err != nil {
		return nil, err
	}
	for public, field := range definition.Fields {
		value := classicInput(input, public)
		if value == nil {
			value = field.InputDefault
		}
		if field.Literal != nil {
			value = field.Literal
		} else if field.Derived {
			value = nil
			if (field.Present != "" && classicInput(input, field.Present) != nil) || (field.Present == "" && input["$value"] == field.When) {
				value = field.DerivedValue
			}
		} else {
			allowed[public] = true
		}
		if field.Ranges != "" {
			allowed[field.Ranges] = true
			if raw := input[field.Ranges]; raw != nil {
				rows, ok := raw.([]any)
				if !ok {
					return nil, errors.New("exclude address ranges must be an array")
				}
				values := []any{}
				if value != nil {
					var ok bool
					values, ok = value.([]any)
					if !ok {
						return nil, errors.New("excluded addresses must be an array")
					}
					values = append([]any(nil), values...)
				}
				for _, row := range rows {
					r, ok := row.(map[string]any)
					if !ok || len(r) != 2 {
						return nil, errors.New("address range requires from and to")
					}
					lo, lok := r["from"].(string)
					hi, hok := r["to"].(string)
					a, ae := netip.ParseAddr(lo)
					b, be := netip.ParseAddr(hi)
					if !lok || !hok || ae != nil || be != nil || !a.Is4() || !b.Is4() || a.Compare(b) > 0 {
						return nil, errors.New("invalid excluded address range")
					}
					values = append(values, lo+"-"+hi)
				}
				value = values
			}
		}
		var variable any
		if field.Variable != "" {
			allowed[field.Variable] = true
			variable = classicInput(input, field.Variable)
		}
		if variable == nil && field.BooleanChoice != "" && value != nil {
			enabled, ok := value.(bool)
			if !ok {
				return nil, fmt.Errorf("%s must be Boolean", public)
			}
			value = nil
			if enabled {
				value = field.BooleanChoice
			}
		}
		leaf := map[string]any{"vipObjectType": field.ObjectType}
		if variable != nil {
			v, ok := variable.(string)
			if !ok || len(v) == 0 || len(v) > 128 {
				return nil, fmt.Errorf("invalid %s variable", public)
			}
			leaf["vipType"] = "variableName"
			leaf["vipVariableName"] = v
		} else if value != nil {
			if field.InputSchema != nil && !validScalarValue(value, field.InputSchema) {
				return nil, fmt.Errorf("invalid %s model value", public)
			}
			if field.Values != nil {
				value = field.Values[value.(string)]
			}
			if field.DecimalString {
				if n, ok := value.(float64); ok {
					value = strconv.FormatFloat(n, 'f', 0, 64)
				}
			}
			if field.WrapList {
				value = []any{value}
			}
			if !validScalarValue(value, field.Schema) {
				return nil, fmt.Errorf("invalid %s value", public)
			}
			if field.Set {
				values := value.([]any)
				unique := map[string]any{}
				for _, v := range values {
					encoded, _ := json.Marshal(v)
					unique[string(encoded)] = v
				}
				keys := []string{}
				for key := range unique {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				values = []any{}
				for _, key := range keys {
					values = append(values, unique[key])
				}
				value = values
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
	if definition.AlwaysPriorityOrder {
		data["priority-order"] = append([]string(nil), definition.PriorityOrder...)
	}
	if definition.Optional != "" {
		allowed[definition.Optional] = true
		if value := input[definition.Optional]; value != nil {
			flag, ok := value.(bool)
			if !ok {
				return nil, errors.New("optional must be Boolean")
			}
			data["vipOptional"] = flag
			data["priority-order"] = append([]string(nil), definition.PriorityOrder...)
		}
	}
	for public, tree := range definition.Trees {
		allowed[public] = true
		if value := classicInput(input, public); value != nil {
			rows, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("%s must be an array", public)
			}
			if len(rows) == 0 {
				continue
			}
			output := []any{}
			identities := map[string]bool{}
			for _, row := range rows {
				object, ok := row.(map[string]any)
				if tree.Primitive {
					object, ok = map[string]any{"$value": row}, true
				}
				if !ok {
					return nil, errors.New("classic rows must be objects")
				}
				mapped, err := classicData(tree.Definition, object, false)
				if err != nil {
					return nil, err
				}
				identity := []any{}
				for _, key := range tree.PrimaryKeys {
					leaf, ok := mapped[key].(map[string]any)
					if !ok || leaf["vipType"] == "ignore" {
						return nil, errors.New("classic row primary key is required")
					}
					identity = append(identity, leaf)
				}
				encoded, _ := json.Marshal(identity)
				if identities[string(encoded)] {
					return nil, errors.New("duplicate classic row primary key")
				}
				identities[string(encoded)] = true
				output = append(output, mapped)
			}
			setScalarPath(data, tree.Path, map[string]any{"vipObjectType": "tree", "vipType": "constant", "vipPrimaryKey": tree.PrimaryKeys, "vipValue": output})
		}
	}
	if err := validateClassicFields(input, allowed, ""); err != nil {
		return nil, err
	}

	return data, nil
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
			if !ok || !keysOK {
				return nil, ErrResponse
			}
			for _, key := range keys {
				if _, ok := key.(string); !ok {
					return nil, ErrResponse
				}
			}
			if object["vipType"] != "constant" && object["vipType"] != "ignore" {
				return nil, ErrResponse
			}
			if object["vipType"] == "ignore" && len(entries) != 0 {
				return nil, ErrResponse
			}
			normalized := []any{}
			for _, entry := range entries {
				if _, ok := entry.(map[string]any); !ok {
					return nil, ErrResponse
				}
				row, err := normalizeVIP(entry)
				if err != nil {
					return nil, err
				}
				normalized = append(normalized, row)
			}
			return map[string]any{"vipObjectType": kind, "vipType": object["vipType"], "vipPrimaryKey": keys, "vipValue": normalized}, nil
		}
		if kind != "object" && kind != "node-only" && kind != "list" {
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

func classicInput(input map[string]any, path string) any {
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		child, ok := input[part].(map[string]any)
		if !ok {
			return nil
		}
		input = child
	}
	return input[parts[len(parts)-1]]
}
func validateClassicFields(input map[string]any, allowed map[string]bool, prefix string) error {
	for key, value := range input {
		path := prefix + key
		if allowed[path] {
			continue
		}
		nested := false
		for field := range allowed {
			if strings.HasPrefix(field, path+".") {
				nested = true
				break
			}
		}
		if nested {
			child, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("%s must be an object", path)
			}
			if err := validateClassicFields(child, allowed, path+"."); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("unsupported classic field %s", path)
		}
	}
	return nil
}
