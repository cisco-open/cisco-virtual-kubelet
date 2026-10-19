// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"testing"
)

func qualifyLiveTemplateBindingMetadata(t *testing.T, c *client, ctx context.Context, projectName, projectID, id, name string, settled *bool) {
	t.Helper()
	for _, mode := range []string{"runtime-binding", "updated-filter", "binding-noop", "clear-binding", "restore-binding"} {
		filter := "RADIUS"
		if mode == "updated-filter" || mode == "binding-noop" {
			filter = "TACACS"
		}
		variable := map[string]any{"name": "AAAEP", "data_type": "STRING", "required": true, "param_array": true, "selection_type": "SINGLE_SELECT", "binding": templateCommonSettingsBinding(t, filter)}
		if mode == "clear-binding" {
			variable["binding"], variable["required"] = "", false
			variable["data_values"] = map[string]any{}
		}
		definition := map[string]any{"name": name, "language": "JINJA", "software_type": "IOS-XE", "device_types": []any{map[string]any{"product_family": "Switches and Hubs"}}, "variables": []any{variable}}
		raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"templates": map[string]any{"projects": []any{map[string]any{"name": projectName, "dayn_templates": []any{definition}}}}}})
		if err != nil {
			t.Fatal("binding model encoding failed")
		}
		files := map[string]string{name + ".j2": "! {{ AAAEP }}\n"}
		intent, err := parseTemplateIntent(raw, files)
		if err != nil {
			t.Fatal("upstream-shaped binding fixture refused", err)
		}
		want := intent.definitions[0].payload
		writes := 2
		if mode == "binding-noop" {
			writes = 0
		}
		qualifyLiveTemplateMetadataTransition(t, c, ctx, raw, files, templateSummary{ID: id, Name: name, ProjectID: projectID}, mode, writes, settled, func(body map[string]any) bool {
			if err := validateTemplateDeploymentParameters(body, nil); err != nil {
				t.Error("native binding declaration blocks omitted runtime parameters", mode, err)
			}
			if err := validateTemplateDeploymentParameters(body, map[string][]string{"AAAEP": {"CVK-unlisted-override"}}); err == nil {
				t.Error("binding bypassed explicit choice membership", mode)
			}
			params, ok := body["templateParams"].([]any)
			if !ok || len(params) != 1 {
				t.Error("native bound parameter shape changed", mode)
				return false
			}
			param, ok := params[0].(map[string]any)
			if !ok {
				return false
			}
			selection, ok := param["selection"].(map[string]any)
			if !ok {
				return false
			}
			choices, ok := selection["selectionValues"].(map[string]any)
			if !ok {
				return false
			}
			t.Log("native choice-map count", mode, len(choices))
			return templateDefinitionMatches(want, body)
		})
		t.Log("native binding metadata transition verified", mode)
	}
}
