// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func qualifyLiveTemplateParameterTypes(t *testing.T, c *client, ctx context.Context, projectName, projectID, id, name string, settled *bool) {
	t.Helper()
	for _, mode := range []string{"scalar-types", "metadata-updated", "metadata-cleared", "cleared-noop", "metadata-restored"} {
		cleared := mode == "metadata-cleared" || mode == "cleared-noop"
		variables := []any{
			map[string]any{"name": "counter", "data_type": "INTEGER", "default_value": 7},
			map[string]any{"name": "address", "data_type": "IPADDRESS", "default_value": "198.51.100.1"},
			map[string]any{"name": "mac", "data_type": "MACADDRESS", "default_value": "00:11:22:33:44:55"},
			map[string]any{"name": "label", "data_type": "STRING", "default_value": "qualification", "not_param": true},
		}
		// The lab drops or retypes SECTIONDIVIDER. Keep its explicit probe
		// available, but never count that negative qualification as support.
		probeDivider := os.Getenv("CATC_TEMPLATE_SECTION_DIVIDER") == "isolated-probe"
		if probeDivider {
			variables = append(variables, map[string]any{"name": "heading", "data_type": "SECTIONDIVIDER", "not_param": true})
		}
		for _, raw := range variables {
			v := raw.(map[string]any)
			v["required"], v["param_array"] = false, false
			if _, present := v["not_param"]; !present {
				v["not_param"] = false
			}
			for _, key := range []string{"additional_info", "field_name", "hint_text"} {
				v[key] = "CVK qualification metadata"
				if mode == "metadata-updated" {
					v[key] = "CVK updated metadata"
				}
				if cleared {
					v[key] = ""
				}
			}
			if v["data_type"] != "SECTIONDIVIDER" {
				v["binding"] = ""
				if cleared {
					v["default_value"] = ""
				}
			}
		}
		if mode == "metadata-updated" {
			variables[0].(map[string]any)["required"] = true
			variables[1].(map[string]any)["param_array"] = true
		}
		definition := map[string]any{"name": name, "language": "JINJA", "software_type": "IOS-XE", "device_types": []any{map[string]any{"product_family": "Switches and Hubs"}}, "variables": variables}
		raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"templates": map[string]any{"projects": []any{map[string]any{"name": projectName, "dayn_templates": []any{definition}}}}}})
		if err != nil {
			t.Fatal("parameter type fixture encoding failed")
		}
		content := "! {{ counter }} {{ address }} {{ mac }} {{ label }}"
		if probeDivider {
			content += " {{ heading }}"
		}
		files := map[string]string{name + ".j2": content + "\n"}
		intent, err := parseTemplateIntent(raw, files)
		if err != nil {
			t.Fatal("parameter type fixture invalid", err)
		}
		want := intent.definitions[0].payload
		writes := 2
		if mode == "cleared-noop" {
			writes = 0
		}
		qualifyLiveTemplateMetadataTransition(t, c, ctx, raw, files, templateSummary{ID: id, Name: name, ProjectID: projectID}, mode, writes, settled, func(body map[string]any) bool {
			if err := validateTemplateDeploymentParameters(body, nil); err != nil {
				t.Error("native defaults/optional type declarations refused", mode, err)
			}
			params := map[string][]string{"counter": {"9"}, "address": {"2001:db8::1"}, "mac": {"0011.2233.4455"}}
			if mode == "metadata-updated" {
				params["address"] = []string{"2001:db8::1", "198.51.100.2"}
			}
			if err := validateTemplateDeploymentParameters(body, params); err != nil {
				t.Error("native typed parameter values refused", mode, err)
			}
			for _, invalid := range []map[string][]string{{"address": {"invalid"}}, {"mac": {"invalid"}}, {"counter": {"1.5"}}, {"label": {"unavailable"}}, {"heading": {"unavailable"}}} {
				if validateTemplateDeploymentParameters(body, invalid) == nil {
					t.Error("invalid typed/nonparameter value accepted", mode)
				}
			}
			return templateDefinitionMatches(want, body)
		})
		t.Log("native parameter types and metadata transition verified", mode)
	}
}
