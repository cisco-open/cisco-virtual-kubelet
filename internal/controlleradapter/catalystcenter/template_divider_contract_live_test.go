// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

// Explicit native representation research on the captured generated template.
// Candidate-only ordering/grouping fields never enter production compilation.
// Failure to preserve a divider is a failed qualification, not normalized success.
func qualifyLiveTemplateDividerContract(t *testing.T, c *client, ctx context.Context, projectName, projectID, id, name string, settled *bool) {
	t.Helper()
	preserved := 0
	for _, mode := range []string{"grouped-divider", "ordered-divider", "ordered-content-reference"} {
		content := "! {{ value }}\n"
		if mode == "ordered-content-reference" {
			content = "! {{ heading }} {{ value }}\n"
		}
		variables := []any{
			map[string]any{"name": "heading", "data_type": "SECTIONDIVIDER", "not_param": true, "field_name": "CVK section"},
			map[string]any{"name": "value", "data_type": "STRING", "default_value": "qualification", "required": false},
		}
		raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"templates": map[string]any{"projects": []any{map[string]any{"name": projectName, "dayn_templates": []any{map[string]any{"name": name, "language": "JINJA", "software_type": "IOS-XE", "device_types": []any{map[string]any{"product_family": "Switches and Hubs"}}, "variables": variables}}}}}}})
		if err != nil {
			t.Fatal("divider fixture encoding failed")
		}
		intent, err := parseTemplateIntent(raw, map[string]string{name + ".j2": content})
		if err != nil || len(intent.definitions) != 1 {
			t.Fatal("divider fixture compilation failed", err)
		}
		payload := intent.definitions[0].payload
		payload["customParamsOrder"] = true
		for i, raw := range payload["templateParams"].([]any) {
			p := raw.(map[string]any)
			p["order"], p["customOrder"] = json.Number(strconv.Itoa(i)), json.Number(strconv.Itoa(i))
			if mode == "grouped-divider" {
				p["group"] = "heading"
				p["key"] = p["parameterName"]
			}
		}
		ex := &templateExecutor{client: c, intent: intent}
		*settled = false
		before, err := c.latestTemplateVersion(ctx, id)
		if err != nil || before == nil {
			t.Fatal("divider baseline version unavailable", err)
		}
		var task Task
		var submitted time.Time
		dispatched := false
		for attempt := 0; attempt < 3; attempt++ {
			write, err := ex.next(ctx)
			if err != nil || write == nil || write.action != "Update" || write.identity.target != id || write.identity.parent != projectID || write.resource != "draft:"+intent.definitions[0].identity.Key() {
				t.Fatal("divider candidate escaped captured draft", err)
			}
			submitted = time.Now()
			task, err = write.submit(ctx)
			if err == nil {
				dispatched = true
				break
			}
			if write.noDispatch == nil || write.noDispatch(err) == nil {
				t.Fatal("divider candidate outcome uncertain; no replay", err)
			}
			t.Log("divider candidate proven not dispatched; fresh plan", mode, attempt+1)
		}
		if !dispatched {
			*settled = true
			t.Error("divider candidate not tested: three fresh plans refused before dispatch", mode)
			continue
		}
		t.Log("divider candidate task", mode, task.ID)
		if err := waitLiveTemplateTaskTree(ctx, c, task, submitted); err != nil {
			t.Fatal("divider task not completed; retain fixture", err)
		}
		body, err := c.readTemplateDefinition(ctx, templateSummary{ID: id, Name: name, ProjectID: projectID})
		if err != nil {
			t.Fatal("divider readback unavailable", err)
		}
		after, err := c.latestTemplateVersion(ctx, id)
		if err != nil || after == nil || *after != *before {
			t.Fatal("draft-only candidate changed committed history", err)
		}
		divider := false
		records, ok := body["templateParams"].([]any)
		if !ok {
			t.Fatal("native candidate parameter list missing; retain fixture")
		}
		for _, raw := range records {
			p, ok := raw.(map[string]any)
			if ok && p["parameterName"] == "heading" && p["dataType"] == "SECTIONDIVIDER" {
				divider = true
			}
		}
		matches := templateDefinitionMatches(payload, body)
		if matches {
			preserved++
		}
		t.Log("divider candidate result", mode, "dividerPreserved", divider, "managedFieldsMatch", matches)
		// A known completed draft and exact identity/history are safe to inspect
		// with another explicit candidate, and for generated-fixture cleanup.
		*settled = true
	}
	if preserved == 0 {
		t.Error("none of the documented-field candidates preserved the complete divider intent; native qualification remains failed")
	} else {
		t.Error("candidate preserved divider intent; committed lifecycle qualification is still required")
	}
}
