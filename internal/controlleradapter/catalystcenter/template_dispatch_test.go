// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
)

// Exercise compiled writes, not just the generic guard. A commit must refuse
// a changed draft even when its UUID and resolved member marker stay the same.
func TestTemplateDefinitionDispatchSnapshot(t *testing.T) {
	for _, stage := range []string{"project", "create", "update", "commit"} {
		for _, change := range []string{"unchanged", "project", "draft", "version", "intent", "read-failure", "lost-receipt", "concurrent"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				ctx := context.Background()
				intent, err := parseTemplateIntent([]byte(templateModel), map[string]string{"base.j2": "first\n"})
				if err != nil {
					t.Fatal(err)
				}
				e := &templateExecutor{intent: intent}
				if stage == "project" {
					description := "updated parent"
					e.intent.projects[0].Description = &description
				}
				var mu sync.Mutex
				projectID := "project"
				var draft map[string]any
				if stage != "create" {
					b, _ := json.Marshal(intent.definitions[0].payload)
					json.Unmarshal(b, &draft)
					draft["id"] = "base"
					draft["projectId"] = "project"
					if stage == "update" {
						draft["templateContent"] = "old\n"
					}
				}
				version := false
				fail := false
				var writes atomic.Int32
				e.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					if r.URL.Path == authPath {
						fmt.Fprint(w, `{"Token":"test"}`)
						return
					}
					if r.Method != "GET" {
						writes.Add(1)
						if change == "lost-receipt" {
							fmt.Fprint(w, `{}`)
							return
						}
						fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
						return
					}
					if fail {
						w.WriteHeader(503)
						return
					}
					switch r.URL.Path {
					case templateProjectsPath:
						json.NewEncoder(w).Encode([]templateProject{{ID: projectID, Name: "Lab"}})
					case templatesPath:
						if draft == nil {
							fmt.Fprint(w, `[]`)
						} else {
							fmt.Fprint(w, `[{"templateId":"base","projectId":"project","name":"base"}]`)
						}
					case templatesPath + "/base":
						json.NewEncoder(w).Encode(draft)
					case templateVersionsPath + "/base":
						if version {
							fmt.Fprint(w, `[{"templateId":"base","versionsInfo":[{"id":"revision","version":"1"}]}]`)
						} else {
							fmt.Fprint(w, `[{"templateId":"base","versionsInfo":[{"id":"base"}]}]`)
						}
					case templatesPath + "/revision":
						b, _ := json.Marshal(draft)
						var body map[string]any
						json.Unmarshal(b, &body)
						body["id"] = "revision"
						body["version"] = "1"
						json.NewEncoder(w).Encode(body)
					default:
						t.Errorf("unexpected read %s", r.URL.Path)
						w.WriteHeader(404)
					}
				})
				write, err := e.next(ctx)
				if err != nil || write == nil {
					t.Fatalf("plan: %v", err)
				}
				if stage == "commit" && write.resource[:7] != "commit:" {
					t.Fatalf("not a commit: %s", write.resource)
				}
				mu.Lock()
				switch change {
				case "project":
					projectID = "replacement"
				case "draft":
					if draft == nil {
						draft = map[string]any{"id": "base", "name": "base", "projectId": "project", "composite": false}
					} else {
						draft["description"] = "external change"
					}
				case "version":
					if draft == nil {
						draft = map[string]any{"id": "base", "name": "base", "projectId": "project", "composite": false}
					}
					version = true
				case "intent":
					e.intent.definitions[0].payload["templateContent"] = "changed source\n"
				case "read-failure":
					fail = true
				}
				mu.Unlock()
				if change == "concurrent" {
					var wg sync.WaitGroup
					var successes atomic.Int32
					for i := 0; i < 8; i++ {
						wg.Add(1)
						go func() {
							defer wg.Done()
							_, err := write.submit(ctx)
							if err == nil {
								successes.Add(1)
							} else if !errors.Is(err, errConfigurationTransition) {
								t.Errorf("duplicate: %v", err)
							}
						}()
					}
					wg.Wait()
					if successes.Load() != 1 {
						t.Fatalf("successful submissions %d", successes.Load())
					}
				} else {
					_, err = write.submit(ctx)
					if change == "unchanged" {
						if err != nil {
							t.Fatal(err)
						}
					} else if err == nil {
						t.Fatal("changed observation dispatched")
					}
				}
				if _, err := write.submit(ctx); !errors.Is(err, errConfigurationTransition) {
					t.Fatalf("replay accepted: %v", err)
				}
				want := int32(0)
				if change == "unchanged" || change == "concurrent" || change == "lost-receipt" {
					want = 1
				}
				if writes.Load() != want {
					t.Fatalf("writes=%d want=%d", writes.Load(), want)
				}
			})
		}
	}
}

func TestTemplateProjectDispatchSnapshot(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, change := range []string{"unchanged", "identity", "description", "intent", "other-selected"} {
			t.Run(fmt.Sprintf("existing=%t/%s", existing, change), func(t *testing.T) {
				var mu sync.Mutex
				projects := []templateProject{{ID: "other", Name: "Other"}}
				description := "old"
				if existing {
					projects = append(projects, templateProject{ID: "project", Name: "Lab", Description: &description})
				}
				var writes atomic.Int32
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					if r.URL.Path == authPath {
						fmt.Fprint(w, `{"Token":"test"}`)
						return
					}
					if r.Method == "GET" {
						json.NewEncoder(w).Encode(projects)
						return
					}
					writes.Add(1)
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
				})
				desired := "new"
				e := &templateProjectExecutor{client: c, desired: []templateProject{{Name: "Lab", Description: &desired}, {Name: "Other"}}}
				write, err := e.next(context.Background())
				if err != nil || write == nil {
					t.Fatalf("plan: %v", err)
				}
				mu.Lock()
				switch change {
				case "identity":
					if existing {
						projects[1].ID = "replacement"
					} else {
						projects = append(projects, templateProject{ID: "replacement", Name: "Lab"})
					}
				case "description":
					if existing {
						description = "external change"
					} else {
						projects = append(projects, templateProject{ID: "project", Name: "Lab"})
					}
				case "intent":
					desired = "changed source"
				case "other-selected":
					projects[0].ID = "replacement"
				}
				mu.Unlock()
				_, err = write.submit(context.Background())
				if change == "unchanged" {
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil {
					t.Fatal("changed project dispatched")
				}
				if _, err := write.submit(context.Background()); !errors.Is(err, errConfigurationTransition) {
					t.Fatalf("replay: %v", err)
				}
				want := int32(0)
				if change == "unchanged" {
					want = 1
				}
				if writes.Load() != want {
					t.Fatalf("writes=%d want=%d", writes.Load(), want)
				}
			})
		}
	}
}

func TestTemplateCompositeDispatchSnapshot(t *testing.T) {
	for _, stage := range []string{"draft", "commit"} {
		for _, change := range []string{"unchanged", "member-metadata", "composite-draft", "during-settle"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				ctx := context.Background()
				intent, err := parseTemplateIntent([]byte(templateModel), map[string]string{"base.j2": "first\n"})
				if err != nil {
					t.Fatal(err)
				}
				member := intent.definitions[0]
				combined := desiredTemplate{identity: member.identity, payload: map[string]any{"name": "combined", "composite": true}}
				combined.identity.Name = "combined"
				combined.identity.Composite = true
				combined.identity.Members = []string{member.identity.Key()}
				intent.definitions = append(intent.definitions, combined)
				e := &templateExecutor{intent: intent}
				var mu sync.Mutex
				clone := func(in map[string]any) map[string]any {
					b, _ := json.Marshal(in)
					var out map[string]any
					json.Unmarshal(b, &out)
					return out
				}
				base := clone(member.payload)
				base["id"] = "base"
				base["projectId"] = "project"
				observed := map[string]templateObservation{member.identity.Key(): {summary: templateSummary{ID: "base"}, draft: clone(member.payload), committed: clone(member.payload), version: &templateVersion{ID: "base-v1"}}}
				payload, _, ready, err := e.resolvedPayload(combined, observed)
				if err != nil || !ready {
					t.Fatal("resolve", err)
				}
				draft := clone(payload)
				draft["id"] = "combined"
				draft["projectId"] = "project"
				if stage == "draft" {
					draft["description"] = "old"
					e.intent.definitions[1].payload["description"] = "new"
				}
				var writes atomic.Int32
				e.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					if r.URL.Path == authPath {
						fmt.Fprint(w, `{"Token":"test"}`)
						return
					}
					if r.Method != "GET" {
						writes.Add(1)
						fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
						return
					}
					switch r.URL.Path {
					case templateProjectsPath:
						fmt.Fprint(w, `[{"id":"project","name":"Lab"}]`)
					case templatesPath:
						fmt.Fprint(w, `[{"templateId":"base","projectId":"project","name":"base"},{"templateId":"combined","projectId":"project","name":"combined"}]`)
					case templatesPath + "/base":
						json.NewEncoder(w).Encode(base)
					case templatesPath + "/combined":
						json.NewEncoder(w).Encode(draft)
					case templateVersionsPath + "/base":
						fmt.Fprint(w, `[{"templateId":"base","versionsInfo":[{"id":"base-v1","version":"1"}]}]`)
					case templateVersionsPath + "/combined":
						fmt.Fprint(w, `[{"templateId":"combined","versionsInfo":[{"id":"combined"}]}]`)
					case templatesPath + "/base-v1":
						body := clone(base)
						body["id"] = "base-v1"
						body["version"] = "1"
						json.NewEncoder(w).Encode(body)
					default:
						t.Errorf("unexpected read %s", r.URL.Path)
						w.WriteHeader(404)
					}
				})
				calls := 0
				e.settle = func(context.Context, string, map[string]templateObservation) error {
					calls++
					if change == "during-settle" && calls == 2 {
						mu.Lock()
						draft["description"] = "external during settle"
						mu.Unlock()
					}
					return nil
				}
				write, err := e.next(ctx)
				if err != nil || write == nil {
					t.Fatalf("plan: %v", err)
				}
				if write.resource != stage+":Lab#combined" {
					t.Fatalf("wrong stage %s", write.resource)
				}
				mu.Lock()
				switch change {
				case "member-metadata":
					base["description"] = "external metadata"
				case "composite-draft":
					draft["description"] = "external edit"
				}
				mu.Unlock()
				_, err = write.submit(ctx)
				if change == "unchanged" {
					if err != nil {
						t.Fatal(err)
					}
				} else if !errors.Is(err, errConfigurationStale) {
					t.Fatalf("stale snapshot: %v", err)
				}
				if calls != 2 {
					t.Fatalf("settle calls=%d", calls)
				}
				if _, err := write.submit(ctx); !errors.Is(err, errConfigurationTransition) {
					t.Fatalf("replay: %v", err)
				}
				want := int32(0)
				if change == "unchanged" {
					want = 1
				}
				if writes.Load() != want {
					t.Fatalf("writes=%d want=%d", writes.Load(), want)
				}
			})
		}
	}
}
