// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestTemplateVersionFootprintProtectsHistory(t *testing.T) {
	for _, mode := range []string{"stable", "large-number-edit", "old-body-edit", "metadata-edit", "new-version", "foreign-body", "duplicate-version", "wrong-group"} {
		t.Run(mode, func(t *testing.T) {
			changed := false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("history observation mutated native state")
					w.WriteHeader(400)
					return
				}
				if r.URL.Path == templateVersionsPath+"/base" {
					group := "base"
					versions := []any{map[string]any{"id": "base"}, map[string]any{"id": "v1", "version": 1, "unknown": "private-metadata"}, map[string]any{"id": "v2", "version": 2}}
					if changed {
						switch mode {
						case "metadata-edit":
							versions[1].(map[string]any)["unknown"] = "edited private metadata"
						case "new-version":
							versions = append(versions, map[string]any{"id": "v3", "version": 3})
						case "duplicate-version":
							versions = append(versions, map[string]any{"id": "v1", "version": 1})
						case "wrong-group":
							group = "foreign"
						}
					}
					json.NewEncoder(w).Encode([]any{map[string]any{"templateId": group, "versionsInfo": versions}})
					return
				}
				id := strings.TrimPrefix(r.URL.Path, templatesPath+"/")
				number := map[string]int{"v1": 1, "v2": 2, "v3": 3}[id]
				if number == 0 {
					t.Error("unexpected history read")
					w.WriteHeader(404)
					return
				}
				body := map[string]any{"id": id, "name": "sample", "projectId": "project", "version": number, "templateContent": "private committed body", "unknownCounter": json.Number("9007199254740992")}
				if changed && id == "v1" {
					if mode == "large-number-edit" {
						body["unknownCounter"] = json.Number("9007199254740993")
					}
					if mode == "old-body-edit" {
						body["templateContent"] = "external edit of old body"
					}
					if mode == "foreign-body" {
						body["projectId"] = "foreign"
					}
				}
				json.NewEncoder(w).Encode(body)
			})
			comparison := &templateInputComparison{key: []byte(strings.Repeat("k", 32)), keyID: strings.Repeat("a", 64)}
			resource := templateCreatorResource("r", "Lab", "sample") + ":i:" + templateComparisonTag + ":" + comparison.keyID
			original, err := c.templateVersionFootprint(context.Background(), templateSummary{ID: "base", Name: "sample", ProjectID: "project"})
			if err != nil {
				t.Fatal(err)
			}
			first, err := templateNativeMAC(comparison, resource, original)
			if err != nil {
				t.Fatal(err)
			}
			changed = true
			next, err := c.templateVersionFootprint(context.Background(), templateSummary{ID: "base", Name: "sample", ProjectID: "project"})
			if mode == "foreign-body" || mode == "duplicate-version" || mode == "wrong-group" {
				if err == nil {
					t.Fatal("invalid history accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			second, err := templateNativeMAC(comparison, resource, next)
			if err != nil {
				t.Fatal(err)
			}
			if (first == second) != (mode == "stable") {
				t.Fatal("historical state change was not detected")
			}
		})
	}
}
