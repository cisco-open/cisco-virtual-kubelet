// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRegularTemplateInventory(t *testing.T) {
	for _, mode := range []string{"defaults", "definition-policy", "device-policy", "target-policy", "qualified", "external", "false-options", "reprovision", "no-target", "assign", "unknown-device", "composite", "onboarding", "duplicate", "duplicate-variable", "unknown-variable", "null-variable", "object-variable", "null-flag", "ambiguous", "bad-policy", "bad-definition-policy", "bad-project", "unknown-target", "unknown-section", "unknown-inventory"} {
		t.Run(mode, func(t *testing.T) {
			definition := map[string]any{"name": "Baseline"}
			project := map[string]any{"name": "Lab", "dayn_templates": []any{definition}}
			projects := []any{project}
			variables := []any{
				map[string]any{"name": "text", "value": "secret-value"},
				map[string]any{"name": "count", "value": 3},
				map[string]any{"name": "flag", "value": true},
				map[string]any{"name": "list", "value": []any{"a", 2, false}},
				map[string]any{"name": "empty", "value": []any{}},
			}
			target := map[string]any{"name": "Baseline", "variables": variables}
			dayn := map[string]any{"regular": []any{target}}
			device := map[string]any{"name": "sw1", "state": "PROVISION", "site": "Global/Lab", "dayn_templates": dayn}
			inventory := map[string]any{"devices": []any{device}}
			switch mode {
			case "definition-policy":
				definition["redeploy_template"] = "ON_CHANGE"
			case "device-policy":
				definition["redeploy_template"] = "NEVER"
				dayn["redeploy_template"] = "ALWAYS"
			case "target-policy":
				definition["redeploy_template"] = "ALWAYS"
				dayn["redeploy_template"] = "ON_CHANGE"
				target["redeploy_template"] = "NEVER"
			case "qualified":
				target["project_name"] = "Lab"
			case "external":
				target["name"] = "Other#External"
			case "false-options":
				target["copying_config"] = false
				target["force_push_template"] = false
			case "reprovision":
				device["state"] = "REPROVISION"
			case "no-target":
				delete(device, "dayn_templates")
			case "assign":
				device["state"] = "ASSIGN"
			case "unknown-device":
				device["unknown"] = true
			case "composite":
				dayn["composite"] = []any{}
			case "onboarding":
				delete(project, "dayn_templates")
				project["onboarding_templates"] = []any{definition}
			case "duplicate":
				dayn["regular"] = []any{target, map[string]any{"name": "Lab#Baseline"}}
			case "duplicate-variable":
				target["variables"] = append(variables, variables[0])
			case "unknown-variable":
				variables[0].(map[string]any)["template_name"] = "Baseline"
			case "null-variable":
				variables[0].(map[string]any)["value"] = nil
			case "object-variable":
				variables[0].(map[string]any)["value"] = map[string]any{"secret": "secret-value"}
			case "null-flag":
				target["copying_config"] = nil
			case "ambiguous":
				projects = append(projects, map[string]any{"name": "Other", "dayn_templates": []any{map[string]any{"name": "Baseline"}}})
			case "bad-policy":
				dayn["redeploy_template"] = "sometimes-secret-value"
			case "bad-definition-policy":
				definition["redeploy_template"] = "sometimes-secret-value"
			case "bad-project":
				target["project_name"] = "Other#Lab"
			case "unknown-target":
				target["unknown"] = true
			case "unknown-section":
				dayn["unknown"] = true
			case "unknown-inventory":
				inventory["unknown"] = true
			}
			raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": inventory}})
			templates, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"templates": map[string]any{"projects": projects}}})
			common, states, targets, err := parseRegularTemplateInventory(raw, templates)
			valid := mode == "defaults" || mode == "definition-policy" || mode == "device-policy" || mode == "target-policy" || mode == "qualified" || mode == "external" || mode == "false-options" || mode == "reprovision"
			if (err == nil) != valid {
				t.Fatal(mode, "unexpected parsing result", err)
			}
			if !valid {
				if len(common.devices) != 0 || states != nil || targets != nil {
					t.Fatal("partial executable intent escaped")
				}
				if strings.Contains(err.Error(), "secret-value") {
					t.Fatal("input leaked")
				}
				return
			}
			if len(common.devices) != 1 || common.devices[0].reference.Name != "sw1" || common.devices[0].site != "Global/Lab" || states["sw1"] != device["state"] || len(targets) != 1 || len(targets["sw1"]) != 1 {
				t.Fatal("inventory projection changed")
			}
			got := targets["sw1"][0]
			policy := "NEVER"
			if mode == "definition-policy" {
				policy = "ON_CHANGE"
			}
			if mode == "device-policy" {
				policy = "ALWAYS"
			}
			key := "Lab#Baseline"
			if mode == "external" {
				key = "Other#External"
			}
			if got.template.Key() != key || got.external != (mode == "external") || got.redeploy != policy || got.copyingConfig != (mode != "false-options") || got.forcePush != (mode != "false-options") {
				t.Fatal("reference/default precedence changed")
			}
			expected := map[string][]string{"text": {"secret-value"}, "count": {"3"}, "flag": {"true"}, "list": {"a", "2", "false"}, "empty": {}}
			if !reflect.DeepEqual(got.parameters, expected) {
				t.Fatal("parameter conversion changed")
			}
			// Public inventory parsing must still reject the unexecuted day-N intent.
			if _, _, err := parseWiredInventoryIntent(raw); err == nil {
				t.Fatal("public parser silently dropped deployment")
			}
		})
	}
}

func TestTemplateDefinitionDeploymentPolicy(t *testing.T) {
	for _, policy := range []string{"NEVER", "ON_CHANGE", "ALWAYS", "null", "occasionally"} {
		raw := []byte(strings.Replace(templateModel, "language: JINJA", "language: JINJA\n            redeploy_template: "+policy, 1))
		intent, err := parseTemplateIntent(raw, map[string]string{"base.j2": "hostname {{ hostname }}"})
		valid := validTemplateRedeploy(policy)
		if (err == nil) != valid {
			t.Fatal("definition policy validation mismatch")
		}
		if !valid {
			continue
		}
		for _, definition := range intent.definitions {
			if _, ok := definition.payload["redeploy_template"]; ok {
				t.Fatal("reconcile policy leaked into native definition")
			}
			if _, ok := definition.payload["redeploy"]; ok {
				t.Fatal("reconcile policy leaked into native definition")
			}
		}
		_, policies, err := templateDeploymentCatalog(raw)
		if err != nil || policies["Lab#base"] != policy {
			t.Fatal("definition and deployment policy disagree")
		}
	}
}
