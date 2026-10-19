// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"
	"testing"
)

func TestTemplateBindingParameters(t *testing.T) {
	for _, mode := range []string{"omitted-runtime", "explicit-value", "explicit-empty", "explicit-nil", "binding-cleared", "binding-null", "binding-wrong-type", "missing-choice-map", "null-choice-map", "static-choice", "wrong-static-choice"} {
		t.Run(mode, func(t *testing.T) {
			selection := map[string]any{"selectionType": "SINGLE_SELECT", "selectionValues": map[string]any{}}
			param := map[string]any{"parameterName": "bound", "dataType": "STRING", "required": true, "paramArray": true, "binding": templateCommonSettingsBinding(t, "RADIUS"), "selection": selection}
			params := map[string][]string{}
			switch mode {
			case "explicit-value":
				params["bound"] = []string{"private override"}
			case "explicit-empty":
				params["bound"] = []string{}
			case "explicit-nil":
				params["bound"] = nil
			case "binding-cleared":
				param["binding"] = ""
			case "binding-null":
				param["binding"] = nil
			case "binding-wrong-type":
				param["binding"] = map[string]any{"provider": "private provider"}
			case "missing-choice-map":
				delete(selection, "selectionValues")
			case "null-choice-map":
				selection["selectionValues"] = nil
			case "static-choice", "wrong-static-choice":
				selection["selectionValues"] = map[string]any{"label": "private allowed"}
				params["bound"] = []string{"private allowed"}
				if mode == "wrong-static-choice" {
					params["bound"] = []string{"private override"}
				}
			}
			err := validateTemplateDeploymentParameters(map[string]any{"templateParams": []any{param}}, params)
			valid := mode == "omitted-runtime" || mode == "static-choice"
			if (err == nil) != valid {
				t.Fatal("controller-bound selection bypassed requiredness or explicit membership", err)
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("binding or parameter data leaked")
			}
		})
	}
}
