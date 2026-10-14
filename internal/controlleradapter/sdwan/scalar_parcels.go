// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

//go:embed definitions/scalar-parcels.json
var scalarDefinitions []byte

type scalarField struct {
	Alternate            string
	ConditionDefault     any    `json:"condition_default"`
	ReferenceKind        string `json:"reference_kind"`
	Prefix               string
	ConditionField       string `json:"condition_field"`
	ConditionValue       any    `json:"condition_value"`
	Reference            string
	Source               string
	Requires             string
	Values               map[string]string
	VariableEncoding     string         `json:"variable_encoding"`
	InputSchema          map[string]any `json:"input_schema"`
	Encode               string
	Path, Type, Variable string
	Schema               map[string]any
}
type scalarList struct {
	Nonempty   string
	Primitive  bool
	OmitEmpty  bool `json:"omit_empty"`
	Path       string
	Definition scalarDefinition
}
type scalarToggle struct {
	OffDefaults map[string]any `json:"off_defaults"`
	Fields      []string
}
type scalarCombinedInput struct{ Source, Field string }
type scalarDefinition struct {
	CombinedLists     map[string][]scalarCombinedInput `json:"combined_lists"`
	Toggles           map[string]scalarToggle
	OutputSchema      map[string]any    `json:"output_schema"`
	ComputedDefaults  map[string]string `json:"computed_defaults"`
	Collection, Path  string
	IgnoreDescription bool `json:"ignore_description"`
	Wrappers          []scalarList
	PublicDefaults    map[string]any `json:"public_defaults"`
	Required          []string
	Lists             map[string]scalarList
	Defaults          map[string]any
	Fields            map[string]scalarField
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
	if e == nil && definition.OutputSchema != nil {
		encoded, err := json.Marshal(body)
		var normalized any
		if err != nil || json.Unmarshal(encoded, &normalized) != nil || !validScalarValue(normalized, definition.OutputSchema) {
			return metadata, nil, errors.New("parcel intent violates required fields or schema combinations")
		}
	}
	return metadata, body, e
}
func applyScalarDefinition(definition scalarDefinition, input map[string]any, metadata bool) (map[string]any, error) {
	values := make(map[string]any, len(input)+len(definition.PublicDefaults))
	for key, value := range definition.PublicDefaults {
		values[key] = value
	}
	for key, value := range input {
		values[key] = value
	}
	for target, sources := range definition.CombinedLists {
		if _, exists := values[target]; exists {
			return nil, errors.New("internal combined field is not a public input")
		}
		combined := []any{}
		for _, source := range sources {
			if value := values[source.Source]; value != nil {
				items, ok := value.([]any)
				if !ok {
					return nil, fmt.Errorf("%s must be an array", source.Source)
				}
				for _, item := range items {
					combined = append(combined, map[string]any{source.Field: item})
				}
			}
			delete(values, source.Source)
		}
		values[target] = combined
	}
	input = values
	b, e := json.Marshal(definition.Defaults)
	if e != nil {
		return nil, e
	}
	var copy map[string]any
	if json.Unmarshal(b, &copy) != nil {
		return nil, ErrResponse
	}
	definition.Defaults = copy
	for _, key := range definition.Required {
		if input[key] == nil {
			return nil, fmt.Errorf("%s is required", key)
		}
	}
	allowed := map[string]bool{}
	if metadata {
		allowed["name"] = true
		allowed["description"] = true
	}
	disabled := map[string]bool{}
	for public, toggle := range definition.Toggles {
		allowed[public] = true
		enabled := false
		if v := input[public]; v != nil {
			var ok bool
			enabled, ok = v.(bool)
			if !ok {
				return nil, fmt.Errorf("%s must be Boolean", public)
			}
		}
		if !enabled {
			mergeScalarDefaults(definition.Defaults, toggle.OffDefaults)
			for _, field := range toggle.Fields {
				disabled[field] = true
			}
		}
	}
	for public, field := range definition.Fields {
		if field.Source != "" {
			public = field.Source
		}
		allowed[public] = true
		var variable any
		if field.Variable != "" {
			allowed[field.Variable] = true
			variable = input[field.Variable]
		}
		value, exists := input[public]
		if field.Alternate != "" {
			allowed[field.Alternate] = true
			if !exists {
				value = input[field.Alternate]
			}
		}
		condition, exists := input[field.ConditionField]
		if !exists {
			condition = field.ConditionDefault
		}
		if field.ConditionField != "" && !reflect.DeepEqual(condition, field.ConditionValue) {
			deleteScalarPath(definition.Defaults, field.Path)
			continue
		}
		if value != nil && field.Prefix != "" {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be a string", public)
			}
			value = field.Prefix + text
		}
		if field.Requires != "" && input[field.Requires] == nil {
			deleteScalarPath(definition.Defaults, field.Path)
			disabled[public] = true
		}
		if disabled[public] {
			if value != nil || variable != nil {
				return nil, fmt.Errorf("%s requires its feature to be enabled", public)
			}
			continue
		}
		if variable != nil {
			name, ok := variable.(string)
			if field.VariableEncoding == "verbatim" {
				if !strings.HasPrefix(name, "{{") || !strings.HasSuffix(name, "}}") {
					return nil, fmt.Errorf("%s requires an upstream variable expression", public)
				}
				name = strings.TrimSuffix(strings.TrimPrefix(name, "{{"), "}}")
			}
			if !ok || len(name) > 124 || !validParcelVariable.MatchString(name) {
				return nil, fmt.Errorf("invalid %s variable", public)
			}
			setScalarPath(definition.Defaults, field.Path, map[string]any{"optionType": "variable", "value": "{{" + name + "}}"})
		} else if value != nil {
			if field.Reference != "" {
				name, ok := value.(string)
				if !ok || name == "" || len(name) > 128 {
					return nil, errors.New("invalid parcel reference name")
				}
				value = parcelPlaceholder(name)
			}
			if field.InputSchema != nil {
				if !validScalarValue(value, field.InputSchema) {
					return nil, fmt.Errorf("invalid %s model value", public)
				}
				if field.Encode == "decimal-string" {
					value = strconv.FormatFloat(value.(float64), 'f', 0, 64)
				} else if field.Encode == "prefix-address" || field.Encode == "prefix-length" {
					prefix, err := netip.ParsePrefix(value.(string))
					if err != nil {
						return nil, fmt.Errorf("invalid %s prefix", public)
					}
					if field.Encode == "prefix-address" {
						value = prefix.Addr().String()
					} else {
						value = float64(prefix.Bits())
					}
				} else if field.Encode == "enum-map" {
					value = field.Values[value.(string)]
				} else if field.Encode == "comma-join" {
					parts := []string{}
					for _, item := range value.([]any) {
						parts = append(parts, strconv.FormatFloat(item.(float64), 'f', 0, 64))
					}
					value = strings.Join(parts, ",")
				} else {
					return nil, errors.New("unsupported scalar encoding")
				}
			}
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
			for index, item := range items {
				object, ok := item.(map[string]any)
				if list.Primitive {
					object, ok = map[string]any{"$value": item}, true
				}
				if !ok {
					return nil, fmt.Errorf("%s entries must be objects", public)
				}
				copy := make(map[string]any, len(object)+len(list.Definition.ComputedDefaults))
				for key, value := range object {
					copy[key] = value
				}
				for field, prefix := range list.Definition.ComputedDefaults {
					if _, exists := copy[field]; !exists {
						suffix := strconv.Itoa(index)
						if vpn := copy["vpn"]; vpn != nil {
							suffix = fmt.Sprint(vpn)
						}
						copy[field] = fmt.Sprintf("%s-%d-%s", prefix, index, suffix)
					}
				}
				mapped, e := applyScalarDefinition(list.Definition, copy, false)
				if e != nil {
					return nil, e
				}
				out = append(out, mapped)
			}
			if len(out) > 0 || !list.OmitEmpty {
				setScalarPath(definition.Defaults, list.Path, out)
			}
		}
	}
	for _, wrapper := range definition.Wrappers {
		subset := map[string]any{}
		for public, field := range wrapper.Definition.Fields {
			if field.Source != "" {
				public = field.Source
			}
			allowed[public] = true
			if value, ok := input[public]; ok {
				subset[public] = value
			}
			if field.Variable != "" {
				allowed[field.Variable] = true
				if value, ok := input[field.Variable]; ok {
					subset[field.Variable] = value
				}
			}
		}
		for public := range wrapper.Definition.Lists {
			allowed[public] = true
			if value, ok := input[public]; ok {
				subset[public] = value
			}
		}
		if wrapper.Nonempty != "" {
			control := input[wrapper.Nonempty]
			if control == nil || control == "" {
				continue
			}
			if _, ok := control.(string); !ok {
				return nil, fmt.Errorf("%s must be a string", wrapper.Nonempty)
			}
		}
		mapped, err := applyScalarDefinition(wrapper.Definition, subset, false)
		if err != nil {
			return nil, err
		}
		setScalarPath(definition.Defaults, wrapper.Path, []any{mapped})
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
	if constraint, ok := schema["not"].(map[string]any); ok && validScalarValue(value, constraint) {
		return false
	}
	if branches, ok := schema["allOf"].([]any); ok {
		for _, branch := range branches {
			if !validScalarValue(value, branch.(map[string]any)) {
				return false
			}
		}
	}
	if condition, ok := schema["if"].(map[string]any); ok {
		branch := "else"
		if validScalarValue(value, condition) {
			branch = "then"
		}
		if constraint, ok := schema[branch].(map[string]any); ok && !validScalarValue(value, constraint) {
			return false
		}
	}
	if constant, exists := schema["const"]; exists && !reflect.DeepEqual(value, constant) {
		return false
	}
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
		case map[string]any:
			kind = "object"
		default:
			return false
		}
	}
	switch kind {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return false
		}
		if min, ok := schema["minProperties"].(float64); ok && float64(len(object)) < min {
			return false
		}
		if max, ok := schema["maxProperties"].(float64); ok && float64(len(object)) > max {
			return false
		}
		if required, ok := schema["required"].([]any); ok {
			for _, key := range required {
				if _, exists := object[key.(string)]; !exists {
					return false
				}
			}
		}
		dependencies := map[string]any{}
		for _, keyword := range []string{"dependencies", "dependentRequired"} {
			if rules, ok := schema[keyword].(map[string]any); ok {
				for key, value := range rules {
					dependencies[key] = value
				}
			}
		}
		if len(dependencies) > 0 {
			for key, dependency := range dependencies {
				if _, exists := object[key]; !exists {
					continue
				}
				switch d := dependency.(type) {
				case map[string]any:
					if !validScalarValue(value, d) {
						return false
					}
				case []any:
					for _, key := range d {
						if _, exists := object[key.(string)]; !exists {
							return false
						}
					}
				default:
					return false
				}
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		for key, field := range object {
			if constraint, exists := properties[key]; exists {
				if !validScalarValue(field, constraint.(map[string]any)) {
					return false
				}
			} else if schema["additionalProperties"] == false {
				return false
			}
		}
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
			case "ip", "ipv4", "ipv6":
				address, e := netip.ParseAddr(v)
				if e != nil || (format == "ipv6" && !address.Is6()) || (format == "ipv4" && !address.Is4()) {
					return false
				}
			case "mac":
				address, e := net.ParseMAC(v)
				if e != nil || len(address) != 6 {
					return false
				}
			case "ipv4Range":
				parts := strings.Split(v, "-")
				if len(parts) > 2 {
					return false
				}
				a, e := netip.ParseAddr(parts[0])
				if e != nil || !a.Is4() {
					return false
				}
				if len(parts) == 2 {
					b, e := netip.ParseAddr(parts[1])
					if e != nil || !b.Is4() || a.Compare(b) > 0 {
						return false
					}
				}
			case "ipv4-prefix", "ipv6-prefix":
				prefix, e := netip.ParsePrefix(v)
				if e != nil || format == "ipv4-prefix" && !prefix.Addr().Is4() || format == "ipv6-prefix" && !prefix.Addr().Is6() {
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
		if schema["uniqueItems"] == true {
			seen := map[string]bool{}
			for _, item := range items {
				encoded, err := json.Marshal(item)
				if err != nil || seen[string(encoded)] {
					return false
				}
				seen[string(encoded)] = true
			}
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

func mergeScalarDefaults(target, source map[string]any) {
	for key, value := range source {
		if object, ok := value.(map[string]any); ok {
			child, ok := target[key].(map[string]any)
			if !ok {
				child = map[string]any{}
				target[key] = child
			}
			mergeScalarDefaults(child, object)
		} else {
			target[key] = value
		}
	}
}

func deleteScalarPath(root map[string]any, path string) {
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		next, ok := root[part].(map[string]any)
		if !ok {
			return
		}
		root = next
	}
	delete(root, parts[len(parts)-1])
}
