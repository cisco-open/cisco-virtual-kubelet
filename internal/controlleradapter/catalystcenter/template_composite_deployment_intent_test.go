// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"reflect"
	"strings"
	"testing"
)

func TestCompositeTemplateTargets(t *testing.T) {
	raw := []byte(`{"catalyst_center":{"templates":{"projects":[{"name":"Lab","dayn_templates":[{"name":"Combined","composite":true,"containing_templates":["Base","Telemetry"],"redeploy_template":"ON_CHANGE"},{"name":"Base"},{"name":"Telemetry"}]}]}}}`)
	index, policies, err := templateDeploymentCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"declared", "external", "device-policy", "target-policy", "false-options", "same-name-different-members", "wrong-kind", "unknown-member", "missing-member", "qualified-member", "duplicate-variable", "duplicate-target", "mixed-section", "unknown-variable-field", "null-value"} {
		t.Run(mode, func(t *testing.T) {
			variable := map[string]any{"template_name": "Base", "name": "value", "value": []any{"a", 2, true}}
			target := map[string]any{"name": "Combined", "variables": []any{variable}}
			section := map[string]any{"composite": []any{target}}
			switch mode {
			case "external":
				target["name"] = "External#Combined"
			case "device-policy":
				section["redeploy_template"] = "ALWAYS"
			case "target-policy":
				section["redeploy_template"] = "ALWAYS"
				target["redeploy_template"] = "NEVER"
			case "false-options":
				target["copying_config"] = false
				target["force_push_template"] = false
			case "same-name-different-members":
				target["variables"] = []any{variable, map[string]any{"template_name": "Telemetry", "name": "value", "value": "b"}}
			case "wrong-kind":
				target["name"] = "Base"
			case "unknown-member":
				variable["template_name"] = "Missing"
			case "missing-member":
				delete(variable, "template_name")
			case "qualified-member":
				variable["template_name"] = "Lab#Base"
			case "duplicate-variable":
				target["variables"] = []any{variable, variable}
			case "duplicate-target":
				section["composite"] = []any{target, target}
			case "mixed-section":
				section["regular"] = []any{map[string]any{"name": "Base"}}
			case "unknown-variable-field":
				variable["extra"] = true
			case "null-value":
				variable["value"] = nil
			}
			got, err := parseCompositeTemplateTargets(section, index, policies)
			valid := mode == "declared" || mode == "external" || mode == "device-policy" || mode == "target-policy" || mode == "false-options" || mode == "same-name-different-members"
			if !valid {
				if err == nil || got != nil {
					t.Fatal("invalid composite target accepted")
				}
				return
			}
			if err != nil || len(got) != 1 || !got[0].template.Composite {
				t.Fatal("valid composite rejected", err)
			}
			wantPolicy := "ON_CHANGE"
			if mode == "external" || mode == "target-policy" {
				wantPolicy = "NEVER"
			}
			if mode == "device-policy" {
				wantPolicy = "ALWAYS"
			}
			if got[0].redeploy != wantPolicy || got[0].copyingConfig != (mode != "false-options") || got[0].forcePush != (mode != "false-options") {
				t.Fatal("incorrect defaults or precedence")
			}
			if !reflect.DeepEqual(got[0].parameters["Base"]["value"], []string{"a", "2", "true"}) {
				t.Fatal("member variable conversion failed")
			}
			if mode == "same-name-different-members" && !reflect.DeepEqual(got[0].parameters["Telemetry"]["value"], []string{"b"}) {
				t.Fatal("member variable namespaces collapsed")
			}
		})
	}
}

func TestMixedTemplateInventoryExtraction(t *testing.T) {
	for _, mode := range []string{"mixed", "composite-only", "unknown", "same-reference", "assign", "missing-serial", "empty-regular"} {
		t.Run(mode, func(t *testing.T) {
			raw := `{"catalyst_center":{"inventory":{"devices":[{"name":"edge","serial_number":"FOC12345678","site":"Global/Lab/Building","state":"PROVISION","dayn_templates":{"regular":[{"name":"Lab#Base"}],"composite":[{"name":"Lab#Combined","variables":[{"template_name":"Base","name":"value","value":3}]}]}}]}}}`
			switch mode {
			case "empty-regular":
				raw = strings.Replace(raw, `"regular":[{"name":"Lab#Base"}]`, `"regular":[]`, 1)
			case "composite-only":
				raw = strings.Replace(raw, `"regular":[{"name":"Lab#Base"}],`, "", 1)
			case "unknown":
				raw = strings.Replace(raw, `"composite":`, `"unknown":`, 1)
			case "same-reference":
				raw = strings.Replace(raw, "Lab#Combined", "Lab#Base", 1)
			case "assign":
				raw = strings.Replace(raw, "PROVISION", "ASSIGN", 1)
			case "missing-serial":
				raw = strings.Replace(raw, `"serial_number":"FOC12345678",`, "", 1)
			}
			_, states, targets, err := parseTemplateInventory([]byte(raw), []byte(raw), true)
			// Identity admission still requires explicit serial later; parsing reuses
			// wired-model support rather than introducing a second identity policy.
			valid := mode == "mixed" || mode == "composite-only" || mode == "missing-serial" || mode == "empty-regular"
			if !valid {
				if err == nil || targets != nil {
					t.Fatal("partial unsupported inventory accepted")
				}
				return
			}
			if err != nil || states["edge"] != "PROVISION" || len(targets["edge"].composite) != 1 {
				t.Fatal("composite intent lost", err)
			}
			if mode != "composite-only" && mode != "empty-regular" && len(targets["edge"].regular) != 1 {
				t.Fatal("regular intent lost")
			}
			if _, _, _, err := parseRegularTemplateInventory([]byte(raw), []byte(raw)); err == nil {
				t.Fatal("legacy route discarded composite intent")
			}
		})
	}
}
