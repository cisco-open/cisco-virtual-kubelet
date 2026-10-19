// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestTemplateVariableMetadata(t *testing.T) {
	for _, mode := range []string{"nullable", "integer-default", "boolean-default", "float-default", "scalar-selections", "empty-default", "empty-selections", "missing-type", "null-type", "unknown-type", "unknown-selection", "null-name", "null-selection-value", "nested-default", "nested-selection", "unknown-null", "string-flag"} {
		t.Run(mode, func(t *testing.T) {
			variable := map[string]any{"name": "value", "data_type": "STRING"}
			valid := true
			switch mode {
			case "nullable":
				for _, key := range []string{"binding", "default_value", "additional_info", "field_name", "hint_text", "not_param", "param_array", "required", "selection_type", "data_values"} {
					variable[key] = nil
				}
			case "integer-default":
				variable["data_type"] = "INTEGER"
				variable["default_value"] = int64(9007199254740993)
			case "boolean-default":
				variable["default_value"] = false
			case "float-default":
				variable["default_value"] = 7.5
			case "scalar-selections":
				variable["selection_type"] = "MULTI_SELECT"
				variable["data_values"] = map[string]any{"integer": 7, "boolean": true, "decimal": 7.5}
			case "empty-default":
				variable["default_value"] = ""
			case "empty-selections":
				variable["data_values"] = map[string]any{}
			case "missing-type":
				delete(variable, "data_type")
				valid = false
			case "null-type":
				variable["data_type"] = nil
				valid = false
			case "unknown-type":
				variable["data_type"] = "BOOLEAN"
				valid = false
			case "unknown-selection":
				variable["selection_type"] = "UNKNOWN"
				valid = false
			case "null-name":
				variable["name"] = nil
				valid = false
			case "null-selection-value":
				variable["data_values"] = map[string]any{"value": nil}
				valid = false
			case "nested-default":
				variable["default_value"] = []any{"private value"}
				valid = false
			case "nested-selection":
				variable["data_values"] = map[string]any{"key": map[string]any{"nested": "private value"}}
				valid = false
			case "unknown-null":
				variable["unsupported_optional"] = nil
				valid = false
			case "string-flag":
				variable["required"] = "true"
				valid = false
			}
			out, err := parseTemplateVariables([]any{variable})
			if !valid {
				if err == nil || out != nil {
					t.Fatal("invalid metadata returned usable parameters")
				}
				if strings.Contains(err.Error(), "private value") {
					t.Fatal("parameter value escaped error")
				}
				return
			}
			if err != nil || len(out) != 1 {
				t.Fatal("provider-compatible metadata refused", err)
			}
			got := out[0].(map[string]any)
			if mode == "nullable" && len(got) != 2 {
				t.Fatal("nullable optional fields were sent to native API")
			}
			defaults := map[string]string{"integer-default": "9007199254740993", "boolean-default": "false", "float-default": "7.5", "empty-default": ""}
			if want, ok := defaults[mode]; ok && got["defaultValue"] != want {
				t.Fatal("scalar coercion changed authored value")
			}
			if mode == "scalar-selections" {
				values := got["selection"].(map[string]any)["selectionValues"].(map[string]any)
				for key, want := range map[string]string{"integer": "7", "boolean": "true", "decimal": "7.5"} {
					if values[key] != want {
						t.Fatal("selection scalar conversion incorrect")
					}
				}
			}
		})
	}
}

func TestTemplateVariableMetadataPublic(t *testing.T) {
	for _, mode := range []string{"nullable-definition", "nullable-variable", "scalar-default", "cleared-variable", "parameter-types", "runtime-binding", "invalid-variable", "invalid-selection"} {
		t.Run(mode, func(t *testing.T) { exerciseTemplateMetadataPublic(t, mode, nil) })
	}
}

func TestTemplateNullableDefinitionFields(t *testing.T) {
	for _, mode := range []string{"optional-null", "language-null", "software-null", "family-null", "unknown-null", "variables-null", "section-tags-null"} {
		t.Run(mode, func(t *testing.T) {
			base := map[string]any{"name": "base", "language": "JINJA", "software_type": "IOS-XE", "device_types": []any{map[string]any{"product_family": "Switches and Hubs"}}}
			project := map[string]any{"name": "Lab", "dayn_templates": []any{base}}
			section := map[string]any{"projects": []any{project}}
			project["description"] = nil
			base["description"] = nil
			base["tags"] = nil
			device := base["device_types"].([]any)[0].(map[string]any)
			device["product_series"], device["product_type"] = nil, nil
			switch mode {
			case "language-null":
				base["language"] = nil
			case "software-null":
				base["software_type"] = nil
			case "family-null":
				device["product_family"] = nil
			case "unknown-null":
				device["unsupported"] = nil
			case "variables-null":
				base["variables"] = nil
			case "section-tags-null":
				section["tags"] = nil
			}
			raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"templates": section}})
			intent, err := parseTemplateIntent(raw, map[string]string{"base.j2": "! metadata\n"})
			if mode != "optional-null" {
				if err == nil {
					t.Fatal("required/unknown/null collection was silently discarded")
				}
				return
			}
			if err != nil || len(intent.projects) != 1 || intent.projects[0].Description != nil || len(intent.definitions) != 1 {
				t.Fatal("provider-compatible optional nulls rejected", err)
			}
			payload := intent.definitions[0].payload
			if _, present := payload["description"]; present {
				t.Fatal("optional null was sent to native API")
			}
			if len(payload["deviceTypes"].([]any)[0].(map[string]any)) != 1 || len(intent.definitions[0].tags) != 0 {
				t.Fatal("nullable selectors/tags became native constraints")
			}
		})
	}
}

func TestTemplateVariableDatatypeContract(t *testing.T) {
	for _, kind := range []string{"STRING", "INTEGER", "IPADDRESS", "MACADDRESS", "SECTIONDIVIDER"} {
		t.Run(kind, func(t *testing.T) {
			out, err := parseTemplateVariables([]any{map[string]any{"name": "value", "data_type": kind, "required": nil, "binding": nil}})
			if err != nil {
				t.Fatal("pinned parameter type refused", err)
			}
			if err := validateTemplateDeploymentParameters(map[string]any{"templateParams": out}, nil); err != nil {
				t.Fatal("definition and deployment type contracts diverged", err)
			}
		})
	}
}

func TestTemplateParameterScalarPrecision(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
		want  string
		valid bool
	}{
		{"integer", int64(9007199254740993), "9007199254740993", true},
		{"unsigned", uint64(math.MaxUint64), "18446744073709551615", true},
		{"authored-string", "9007199254740993.0", "9007199254740993.0", true},
		{"safe-float", float64(9007199254740991), "9007199254740991", true},
		{"unsafe-float", float64(9007199254740993), "", false},
		{"nan", math.NaN(), "", false}, {"infinite", math.Inf(1), "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := templateParameterScalar(test.value)
			if (err == nil) != test.valid || got != test.want {
				t.Fatal("scalar precision contract incorrect")
			}
		})
	}
	// Exercise source decoding, not only already-typed helper inputs.
	raw := strings.Replace(templateModel, "required: true", "default_value: 9007199254740993\n                required: true", 1)
	intent, err := parseTemplateIntent([]byte(raw), map[string]string{"base.j2": "! metadata\n"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(intent.definitions[0].payload)
	if !strings.Contains(string(encoded), `"defaultValue":"9007199254740993"`) {
		t.Fatal("source decoder lost integer precision")
	}
}
