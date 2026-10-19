// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"testing"
)

func qualifyLiveTemplateVariableMetadata(t *testing.T, c *client, ctx context.Context, projectName, projectID, id, name string, settled *bool) {
	t.Helper()
	for _, mode := range []string{"typed-defaults", "updated-defaults", "equivalent-string-inputs", "clear-defaults-and-choices", "cleared-noop", "restore-choices", "remove-one-choice", "multi-selection", "multi-selection-noop", "single-selection-restored"} {
		counter, enabled, threshold := any(7), any(true), any(7.5)
		if mode != "typed-defaults" {
			counter, enabled, threshold = 8, false, 8.5
		}
		choices := map[string]any{"enabled": true, "disabled": false}
		thresholds := map[string]any{"low": 7.5, "high": 8.5}
		if mode == "equivalent-string-inputs" {
			counter, enabled, threshold = "8", "false", "8.5"
			choices = map[string]any{"enabled": "true", "disabled": "false"}
			thresholds = map[string]any{"low": "7.5", "high": "8.5"}
		}
		if mode == "clear-defaults-and-choices" || mode == "cleared-noop" {
			enabled, threshold = "", ""
			choices, thresholds = map[string]any{}, map[string]any{}
		}
		if mode == "remove-one-choice" {
			choices = map[string]any{"disabled": false}
			thresholds = map[string]any{"high": 8.5}
		}
		variables := []any{
			map[string]any{"name": "counter", "data_type": "INTEGER", "default_value": counter, "required": true, "selection_type": nil, "data_values": nil},
			map[string]any{"name": "enabled", "data_type": "STRING", "default_value": enabled, "selection_type": "SINGLE_SELECT", "data_values": choices},
			map[string]any{"name": "threshold", "data_type": "STRING", "default_value": threshold, "selection_type": "SINGLE_SELECT", "data_values": thresholds},
		}
		if mode == "multi-selection" || mode == "multi-selection-noop" {
			variables[1].(map[string]any)["selection_type"] = "MULTI_SELECT"
			variables[2].(map[string]any)["selection_type"] = "MULTI_SELECT"
		}
		if mode != "equivalent-string-inputs" {
			for _, raw := range variables {
				v := raw.(map[string]any)
				for _, key := range []string{"binding", "additional_info", "field_name", "hint_text", "not_param", "param_array"} {
					v[key] = nil
				}
			}
		}
		definition := map[string]any{"name": name, "language": "JINJA", "software_type": "IOS-XE", "device_types": []any{map[string]any{"product_family": "Switches and Hubs"}}, "variables": variables}
		raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"templates": map[string]any{"projects": []any{map[string]any{"name": projectName, "dayn_templates": []any{definition}}}}}})
		files := map[string]string{name + ".j2": "! {{ counter }} {{ enabled }} {{ threshold }}\n"}
		intent, err := parseTemplateIntent(raw, files)
		if err != nil {
			t.Fatal("variable metadata fixture invalid", err)
		}
		want := intent.definitions[0].payload
		writes := 2
		if mode == "equivalent-string-inputs" || mode == "cleared-noop" || mode == "multi-selection-noop" {
			writes = 0
		}
		qualifyLiveTemplateMetadataTransition(t, c, ctx, raw, files, templateSummary{ID: id, Name: name, ProjectID: projectID}, mode, writes, settled, func(body map[string]any) bool {
			// Read-only validation of actual native draft and committed declarations.
			// Record a failure without interrupting completed fixture cleanup.
			if err := validateTemplateDeploymentParameters(body, nil); err != nil {
				t.Error("native optional/default declaration blocks omitted parameters", mode, err)
			}
			params := map[string][]string{"counter": {"9"}, "enabled": {"false"}, "threshold": {"8.5"}}
			if mode == "multi-selection" || mode == "multi-selection-noop" {
				params["enabled"], params["threshold"] = []string{"true", "false"}, []string{"7.5", "8.5"}
			}
			cleared := mode == "clear-defaults-and-choices" || mode == "cleared-noop"
			if err := validateTemplateDeploymentParameters(body, params); (err == nil) == cleared {
				t.Error("native selected value validation inconsistent with declared choices", mode)
			}
			return templateDefinitionMatches(want, body)
		})
		t.Log("native variable metadata, scalar coercion and no-op verified", mode)
	}
}
