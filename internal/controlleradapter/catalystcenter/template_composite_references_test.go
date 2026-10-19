// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTemplateCompositeReferenceHistory(t *testing.T) {
	for _, mode := range []string{"empty", "draft-base", "old-version", "current-version", "cross-project", "unrelated-text", "target-replaced", "missing-kind", "missing-members", "null-members", "duplicate-members", "bad-member", "regular-members", "missing-history", "foreign-version-parent", "late-member-same-count", "late-old-version", "late-target-version", "consumer-removed"} {
		t.Run(mode, func(t *testing.T) {
			var lists atomic.Int32
			target := templateSummary{ID: "target", ProjectID: "p", Name: "target"}
			source := templateSummary{ID: "consumer", ProjectID: "p", Name: "consumer"}
			if mode == "cross-project" {
				source.ProjectID = "q"
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("reference collector performed a mutation")
					w.WriteHeader(400)
					return
				}
				iteration := lists.Load()
				if r.URL.Path == templatesPath {
					iteration = lists.Add(1)
					rows := []templateSummary{target, source, {ID: "other", Name: "other", ProjectID: "p"}}
					if mode == "target-replaced" {
						rows[0].ID = "replacement"
					}
					if mode == "consumer-removed" && iteration > 1 {
						rows = append(rows[:1], rows[2:]...)
					}
					json.NewEncoder(w).Encode(rows)
					return
				}
				if strings.HasPrefix(r.URL.Path, templateVersionsPath+"/") {
					base := strings.TrimPrefix(r.URL.Path, templateVersionsPath+"/")
					if mode == "missing-history" && base == source.ID {
						w.WriteHeader(503)
						return
					}
					versions := []map[string]any{{"id": base}}
					if base == "target" || base == "consumer" {
						versions = append(versions, map[string]any{"id": base + "-v1", "version": "1"})
						if mode != "late-target-version" || base != "target" || iteration > 1 {
							versions = append(versions, map[string]any{"id": base + "-v2", "version": "2"})
						}
					}
					json.NewEncoder(w).Encode([]any{map[string]any{"templateId": base, "versionsInfo": versions}})
					return
				}
				id := strings.TrimPrefix(r.URL.Path, templatesPath+"/")
				base := strings.Split(id, "-v")[0]
				name, project := base, "p"
				if base == "consumer" {
					name, project = source.Name, source.ProjectID
				}
				body := map[string]any{"id": id, "name": name, "projectId": project, "composite": base == "consumer", "templateContent": "private content with target-v1 text", "parameters": map[string]any{"private": "target"}}
				if parts := strings.Split(id, "-v"); len(parts) == 2 {
					body["version"] = parts[1]
				}
				members := []any{}
				if base == "consumer" {
					switch mode {
					case "draft-base":
						if id == base {
							members = append(members, map[string]any{"id": "target"})
						}
					case "old-version", "cross-project", "consumer-removed":
						if id == base+"-v1" {
							members = append(members, map[string]any{"id": "target-v1"})
						}
					case "current-version":
						if id == base+"-v2" {
							members = append(members, map[string]any{"id": "target"})
						}
					case "late-member-same-count":
						if id == base {
							member := "other"
							if iteration > 1 {
								member = "target"
							}
							members = append(members, map[string]any{"id": member})
						}
					case "late-old-version":
						if id == base+"-v1" && iteration > 1 {
							members = append(members, map[string]any{"id": "target-v1"})
						}
					case "late-target-version":
						if id == base+"-v1" {
							members = append(members, map[string]any{"id": "target-v2"})
						}
					case "duplicate-members":
						members = []any{map[string]any{"id": "target"}, map[string]any{"id": "target"}}
					case "bad-member":
						members = []any{map[string]any{"id": "../target"}}
					}
					body["containingTemplates"] = members
					switch mode {
					case "missing-kind":
						delete(body, "composite")
					case "missing-members":
						delete(body, "containingTemplates")
					case "null-members":
						body["containingTemplates"] = nil
					case "foreign-version-parent":
						if id == base+"-v1" {
							body["projectId"] = "foreign"
						}
					}
				}
				if mode == "regular-members" && base == "other" {
					body["containingTemplates"] = []any{map[string]any{"id": "target"}}
				}
				json.NewEncoder(w).Encode(body)
			})
			got, err := c.stableTemplateCompositeReferences(context.Background(), target)
			positive := mode == "empty" || mode == "draft-base" || mode == "old-version" || mode == "current-version" || mode == "cross-project" || mode == "unrelated-text"
			if positive {
				want := 1
				if mode == "empty" || mode == "unrelated-text" {
					want = 0
				}
				if err != nil || len(got) != want {
					t.Fatal("complete structural reference collection failed", err, len(got))
				}
				if mode == "old-version" && got[0].VersionID != "consumer-v1" {
					t.Fatal("old committed reference was lost")
				}
				raw, _ := json.Marshal(got)
				if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "parameters") {
					t.Fatal("template values entered reference snapshot")
				}
			} else if err == nil || got != nil {
				t.Fatal("incomplete or changing graph acquired absence authority")
			}
		})
	}
}
