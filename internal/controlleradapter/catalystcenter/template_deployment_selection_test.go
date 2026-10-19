// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"
	"testing"
)

func TestTemplateDeploymentSelection(t *testing.T) {
	for _, mode := range []string{"single", "multi", "default", "optional-empty", "label", "unknown", "duplicate", "single-many", "bad-kind", "missing-values", "numeric-value", "bad-default", "many-defaults", "required-empty", "bad-shape"} {
		t.Run(mode, func(t *testing.T) {
			selection := map[string]any{"selectionType": "SINGLE_SELECT", "selectionValues": map[string]any{"Label A": "secret-a", "Label B": "secret-b"}}
			declaration := map[string]any{"parameterName": "param", "dataType": "STRING", "required": true, "selection": selection}
			parameters := map[string][]string{"param": {"secret-a"}}
			switch mode {
			case "multi":
				selection["selectionType"] = "MULTI_SELECT"
				parameters["param"] = []string{"secret-a", "secret-b"}
			case "default":
				delete(parameters, "param")
				selection["defaultSelectedValues"] = []any{"secret-a"}
			case "optional-empty":
				declaration["required"] = false
				selection["selectionType"] = "MULTI_SELECT"
				parameters["param"] = []string{}
			case "label":
				parameters["param"] = []string{"Label A"}
			case "unknown":
				parameters["param"] = []string{"secret-other"}
			case "duplicate":
				selection["selectionType"] = "MULTI_SELECT"
				parameters["param"] = []string{"secret-a", "secret-a"}
			case "single-many":
				declaration["paramArray"] = true
				parameters["param"] = []string{"secret-a", "secret-b"}
			case "bad-kind":
				selection["selectionType"] = "UNKNOWN"
			case "missing-values":
				delete(selection, "selectionValues")
			case "numeric-value":
				selection["selectionValues"] = map[string]any{"label": 42}
			case "bad-default":
				selection["defaultSelectedValues"] = []any{"secret-other"}
			case "many-defaults":
				selection["defaultSelectedValues"] = []any{"secret-a", "secret-b"}
			case "required-empty":
				selection["selectionType"] = "MULTI_SELECT"
				parameters["param"] = []string{}
			case "bad-shape":
				declaration["selection"] = []any{}
			}
			err := validateTemplateDeploymentParameters(map[string]any{"templateParams": []any{declaration}}, parameters)
			valid := mode == "single" || mode == "multi" || mode == "default" || mode == "optional-empty"
			if (err == nil) != valid {
				t.Fatal(mode, "unexpected selection validation", err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("selection data leaked")
			}
		})
	}
}
