// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

//go:embed definitions/scalar-parcels.json
var scalarDefinitions []byte

type scalarField struct {
	Path, Type, Variable string
	Schema               map[string]any
}
type scalarList struct {
	Path       string
	Definition scalarDefinition
}
type scalarDefinition struct {
	Lists    map[string]scalarList
	Defaults map[string]any
	Fields   map[string]scalarField
}

func scalarParcelData(kind string, input map[string]any) (ParcelMetadata, map[string]any, error) {
	var definitions struct{ Parcels map[string]scalarDefinition }
	if json.Unmarshal(scalarDefinitions, &definitions) != nil {
		return ParcelMetadata{}, nil, errors.New("invalid embedded parcel definitions")
	}
	definition, ok := definitions.Parcels[kind]
	if !ok {
		return ParcelMetadata{}, nil, errors.New("unsupported scalar parcel")
	}
	metadata := ParcelMetadata{}
	if v, exists := input["name"]; exists {
		name, ok := v.(string)
		if !ok {
			return metadata, nil, errors.New("parcel name must be a string")
		}
		metadata.Name = &name
	}
	if v := input["description"]; v != nil {
		description, ok := v.(string)
		if !ok {
			return metadata, nil, errors.New("parcel description must be a string")
		}
		metadata.Description = description
	}
	body, e := applyScalarDefinition(definition, input, true)
	return metadata, body, e
}
func applyScalarDefinition(definition scalarDefinition, input map[string]any, metadata bool) (map[string]any, error) {
	b, e := json.Marshal(definition.Defaults)
	if e != nil {
		return nil, e
	}
	var copy map[string]any
	if json.Unmarshal(b, &copy) != nil {
		return nil, ErrResponse
	}
	definition.Defaults = copy
	allowed := map[string]bool{}
	if metadata {
		allowed["name"] = true
		allowed["description"] = true
	}
	for public, field := range definition.Fields {
		allowed[public] = true
		var variable any
		if field.Variable != "" {
			allowed[field.Variable] = true
			variable = input[field.Variable]
		}
		value := input[public]
		if variable != nil {
			name, ok := variable.(string)
			if !ok || len(name) > 124 || !validParcelVariable.MatchString(name) {
				return nil, fmt.Errorf("invalid %s variable", public)
			}
			setScalarPath(definition.Defaults, field.Path, map[string]any{"optionType": "variable", "value": "{{" + name + "}}"})
		} else if value != nil {
			if !validScalarValue(value, field.Schema) {
				return nil, fmt.Errorf("invalid %s value", public)
			}
			if field.Type == "Int64" {
				n, ok := value.(float64)
				if !ok || math.Trunc(n) != n {
					return nil, fmt.Errorf("%s requires an integer", public)
				}
			}
			if field.Type == "Set" {
				items, ok := value.([]any)
				if !ok {
					return nil, ErrResponse
				}
				seen := map[string]any{}
				for _, item := range items {
					b, _ := json.Marshal(item)
					seen[string(b)] = item
				}
				keys := []string{}
				for k := range seen {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				values := []any{}
				for _, k := range keys {
					values = append(values, seen[k])
				}
				value = values
			}
			setScalarPath(definition.Defaults, field.Path, map[string]any{"optionType": "global", "value": value})
		}
	}
	for public, list := range definition.Lists {
		allowed[public] = true
		if v := input[public]; v != nil {
			items, ok := v.([]any)
			if !ok {
				return nil, fmt.Errorf("%s must be an array", public)
			}
			out := []any{}
			for _, item := range items {
				object, ok := item.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("%s entries must be objects", public)
				}
				mapped, e := applyScalarDefinition(list.Definition, object, false)
				if e != nil {
					return nil, e
				}
				out = append(out, mapped)
			}
			setScalarPath(definition.Defaults, list.Path, out)
		}
	}
	for key := range input {
		if !allowed[key] {
			return nil, fmt.Errorf("unsupported parcel field %s", key)
		}
	}
	return definition.Defaults, nil
}

var validParcelVariable = regexp.MustCompile(`^[./\[\]a-zA-Z0-9_-]+$`)

func setScalarPath(root map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		m, ok := root[part].(map[string]any)
		if !ok {
			m = map[string]any{}
			root[part] = m
		}
		root = m
	}
	root[parts[len(parts)-1]] = value
}

// The generated value schemas use these scalar/array constraints. Unsupported
// schema keywords that affect validation are rejected by the generator.
func validScalarValue(value any, schema map[string]any) bool {
	if alternatives, ok := schema["oneOf"].([]any); ok {
		count := 0
		for _, alternative := range alternatives {
			if validScalarValue(value, alternative.(map[string]any)) {
				count++
			}
		}
		if count != 1 {
			return false
		}
	}
	if alternatives, ok := schema["anyOf"].([]any); ok {
		matched := false
		for _, alternative := range alternatives {
			if validScalarValue(value, alternative.(map[string]any)) {
				matched = true
			}
		}
		if !matched {
			return false
		}
	}
	kind := schema["type"]
	if kind == nil {
		switch value.(type) {
		case string:
			kind = "string"
		case float64:
			kind = "number"
		case bool:
			kind = "boolean"
		case []any:
			kind = "array"
		default:
			return false
		}
	}
	switch kind {
	case "boolean":
		if _, ok := value.(bool); !ok {
			return false
		}
	case "integer", "number":
		n, ok := value.(float64)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return false
		}
		if kind == "integer" && math.Trunc(n) != n {
			return false
		}
		if divisor, ok := schema["multipleOf"].(float64); ok && (divisor <= 0 || math.Abs(n/divisor-math.Round(n/divisor)) > 1e-9) {
			return false
		}
		if min, ok := schema["minimum"].(float64); ok && n < min {
			return false
		}
		if max, ok := schema["maximum"].(float64); ok && n > max {
			return false
		}
	case "string":
		v, ok := value.(string)
		if !ok {
			return false
		}
		if format, ok := schema["format"].(string); ok {
			switch format {
			case "ipv6":
				address, e := netip.ParseAddr(v)
				if e != nil || !address.Is6() {
					return false
				}
			case "hostname":
				if !validHostname(v) {
					return false
				}
			default:
				return false
			}
		}
		n := float64(utf8.RuneCountInString(v))
		if min, ok := schema["minLength"].(float64); ok && n < min {
			return false
		}
		if max, ok := schema["maxLength"].(float64); ok && n > max {
			return false
		}
		if pattern, ok := schema["pattern"].(string); ok {
			re, e := regexp.Compile(pattern)
			if e != nil || !re.MatchString(v) {
				return false
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return false
		}
		if min, ok := schema["minItems"].(float64); ok && float64(len(items)) < min {
			return false
		}
		if max, ok := schema["maxItems"].(float64); ok && float64(len(items)) > max {
			return false
		}
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for _, item := range items {
				if !validScalarValue(item, itemSchema) {
					return false
				}
			}
		}
	default:
		return false
	}
	if enums, ok := schema["enum"].([]any); ok {
		for _, candidate := range enums {
			if reflect.DeepEqual(candidate, value) {
				return true
			}
		}
		return false
	}
	return true
}

func validHostname(value string) bool {
	value = strings.TrimSuffix(value, ".")
	if value == "" || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
