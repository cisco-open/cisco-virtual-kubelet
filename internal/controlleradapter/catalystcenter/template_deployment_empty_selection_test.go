// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"
	"testing"
)

func TestTemplateDeploymentEmptySelection(t *testing.T) {
	for _, kind := range []string{"SINGLE_SELECT", "MULTI_SELECT"} {
		for _, mode := range []string{"omitted", "supplied", "empty-value", "empty-list", "required-omitted", "required-empty-list", "retained-default", "nonempty-default-selection", "empty-default-selection", "null-default-selection", "malformed-default-selection", "missing-values", "null-values", "array-values"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				selection := map[string]any{"selectionType": kind, "selectionValues": map[string]any{}}
				declaration := map[string]any{"parameterName": "value", "dataType": "STRING", "selection": selection}
				parameters := map[string][]string{}
				valid := mode == "omitted" || mode == "retained-default" || mode == "empty-default-selection" || mode == "null-default-selection" || kind == "MULTI_SELECT" && mode == "empty-list"
				switch mode {
				case "supplied":
					parameters["value"] = []string{"private selection"}
				case "empty-value":
					parameters["value"] = []string{""}
				case "empty-list":
					parameters["value"] = []string{}
				case "required-omitted":
					declaration["required"] = true
				case "required-empty-list":
					declaration["required"] = true
					parameters["value"] = []string{}
				case "retained-default":
					declaration["required"], declaration["defaultValue"] = true, "private default"
				case "nonempty-default-selection":
					selection["defaultSelectedValues"] = []any{"private selection"}
				case "empty-default-selection":
					selection["defaultSelectedValues"] = []any{}
				case "null-default-selection":
					selection["defaultSelectedValues"] = nil
				case "malformed-default-selection":
					selection["defaultSelectedValues"] = "private selection"
				case "missing-values":
					delete(selection, "selectionValues")
				case "null-values":
					selection["selectionValues"] = nil
				case "array-values":
					selection["selectionValues"] = []any{}
				}
				err := validateTemplateDeploymentParameters(map[string]any{"templateParams": []any{declaration}}, parameters)
				if (err == nil) != valid {
					t.Fatal("empty choices changed required/cardinality/membership validation", err)
				}
				if err != nil && strings.Contains(err.Error(), "private") {
					t.Fatal("selection data leaked")
				}
			})
		}
	}
}
