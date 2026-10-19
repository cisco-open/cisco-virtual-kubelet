// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestTagEmptyLogicalGroupRejectedBeforeRequests(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid later rule reached the native API")
		w.WriteHeader(400)
	})
	model := []byte(`{"catalyst_center":{"templates":{"tags":[{"name":"earlier"},{"name":"invalid-later","dynamic_rules":[{"member_type":"networkdevice","operation":"AND","items":[]}]}]}}}`)
	if _, err := c.compileConfigurationExecutor(model, "templates"); err == nil {
		t.Fatal("empty logical group was not caught during complete-source compilation")
	}
}

func TestTagPlansRevalidateNativeState(t *testing.T) {
	for _, mode := range []string{"valid", "create-race", "replacement", "protection-flip", "preserved-rule-change", "later-protection-flip", "unrelated-change", "frozen", "frozen-dynamic", "missing-receipt"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			wanted, err := parseTemplateTags([]any{map[string]any{"name": "selected", "description": "desired"}, map[string]any{"name": "later", "description": "desired"}})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "frozen-dynamic" {
				parsed, err := parseTemplateTags([]any{map[string]any{"name": "selected", "description": "desired", "dynamic_rules": []any{map[string]any{"member_type": "networkdevice", "operation": "AND", "items": []any{map[string]any{"operation": "EQ", "name": "hostname", "value": "selected-rule"}, map[string]any{"operation": "EQ", "name": "family", "value": "Switches and Hubs"}}}}}})
				if err != nil {
					t.Fatal(err)
				}
				wanted[0] = parsed[0]
			}
			falseValue := false
			oldDescription := "old"
			tags := []observedTag{{ID: "selected", Name: "selected", Description: &oldDescription, SystemTag: &falseValue, DynamicRules: []any{map[string]any{"memberType": "networkdevice", "rules": map[string]any{"operation": "EQ", "name": "hostname", "value": "unmanaged-rule"}}}}, {ID: "later", Name: "later", Description: &oldDescription, SystemTag: &falseValue}}
			if mode == "create-race" {
				tags = tags[1:]
			}
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != tagsPath {
					t.Error("unexpected endpoint")
				}
				if r.Method == "GET" {
					json.NewEncoder(w).Encode(map[string]any{"response": tags})
					return
				}
				writes++
				var body observedTag
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.Name != "selected" || body.ID != "selected" || body.Description == nil || *body.Description != "desired" || len(body.DynamicRules) != 1 {
					t.Error("planned update changed or lost preserved rule")
				}
				if mode == "frozen-dynamic" && len(body.DynamicRules) == 1 {
					rule := body.DynamicRules[0].(map[string]any)["rules"].(map[string]any)["items"].([]any)[0].(map[string]any)
					if rule["value"] != "selected-rule" {
						t.Error("nested caller mutation changed the frozen rule")
					}
				}
				if mode == "missing-receipt" {
					fmt.Fprint(w, `{}`)
				} else {
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
				}
			})
			write, err := (&tagExecutor{client: c, desired: wanted}).next(ctx)
			if err != nil || write == nil {
				t.Fatal(err)
			}
			trueValue := true
			switch mode {
			case "create-race":
				tags = append(tags, observedTag{ID: "foreign", Name: "selected", SystemTag: &falseValue})
			case "replacement":
				tags[0].ID = "replacement"
			case "protection-flip":
				tags[0].SystemTag = &trueValue
			case "preserved-rule-change":
				tags[0].DynamicRules = []any{map[string]any{"memberType": "networkdevice", "rules": map[string]any{"operation": "EQ", "name": "hostname", "value": "new-external-rule"}}}
			case "later-protection-flip":
				tags[1].SystemTag = &trueValue
			case "unrelated-change":
				tags = append(tags, observedTag{ID: "unrelated", Name: "unrelated", SystemTag: &trueValue})
			case "frozen-dynamic":
				wanted[0].payload["dynamicRules"].([]any)[0].(map[string]any)["rules"].(map[string]any)["items"].([]any)[0].(map[string]any)["value"] = "caller-mutated-rule"
			case "frozen":
				wanted[0].payload["description"] = "caller-mutated"
				wanted[1].payload["description"] = "caller-mutated-later"
			}
			_, err = write.submit(ctx)
			valid := mode == "valid" || mode == "unrelated-change" || mode == "frozen" || mode == "frozen-dynamic"
			if (err == nil) != valid {
				t.Fatal("unexpected revalidation result", err)
			}
			expected := 0
			if valid || mode == "missing-receipt" {
				expected = 1
			}
			if writes != expected {
				t.Fatal("stale request wrote or native request replayed")
			}
			if _, err := write.submit(ctx); err == nil || writes != expected {
				t.Fatal("consumed tag plan replayed")
			}
		})
	}
}
