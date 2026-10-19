// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"testing"
)

// Only edits the captured generated template's applicability metadata and inert
// comment content. No template deployment or device operation is submitted.
func qualifyLiveTemplateSoftwareVersions(t *testing.T, c *client, ctx context.Context, projectName, projectID, id, name string, settled *bool) {
	t.Helper()
	tests := []struct {
		name                                   string
		fallback, override                     any
		omitFallback, hasOverride, editContent bool
		expected                               string
		writes                                 int
	}{
		{name: "inherited", fallback: "17.18.3", expected: "17.18.3", writes: 2},
		{name: "override", fallback: "17.18.3", hasOverride: true, override: "26.2.1", expected: "26.2.1", writes: 2},
		{name: "null-override-with-content-update", fallback: "17.18.3", hasOverride: true, override: nil, editContent: true, expected: "26.2.1", writes: 2},
		{name: "return-to-inherited", fallback: "17.18.3", expected: "17.18.3", writes: 2},
		{name: "explicit-clear", fallback: "17.18.3", hasOverride: true, override: "", expected: "", writes: 2},
		{name: "empty-fallback", fallback: "", expected: "", writes: 0},
		{name: "null-fallback", fallback: nil, expected: "", writes: 0},
		{name: "omitted", omitFallback: true, expected: "", writes: 0},
	}
	content := "! CVK isolated qualification updated\n"
	for _, test := range tests {
		*settled = false
		if test.editContent {
			content = "! CVK software-version retention qualification\n"
		}
		definition := map[string]any{"name": name, "language": "JINJA", "software_type": "IOS-XE", "device_types": []any{map[string]any{"product_family": "Switches and Hubs"}}, "variables": []any{}}
		if test.hasOverride {
			definition["software_version"] = test.override
		}
		section := map[string]any{"projects": []any{map[string]any{"name": projectName, "dayn_templates": []any{definition}}}}
		if !test.omitFallback {
			section["software_version"] = test.fallback
		}
		raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"templates": section}})
		qualifyLiveTemplateMetadataTransition(t, c, ctx, raw, map[string]string{name + ".j2": content}, templateSummary{ID: id, Name: name, ProjectID: projectID}, test.name, test.writes, settled, func(body map[string]any) bool {
			return templateDefinitionMatches(map[string]any{"softwareVersion": test.expected}, body)
		})
		*settled = true
		t.Log("native software-version fallback/readback/no-op verified", test.name)
	}
}
