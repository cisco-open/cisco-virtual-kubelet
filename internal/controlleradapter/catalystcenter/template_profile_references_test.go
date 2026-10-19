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

func TestTemplateAttachedProfilesCompleteness(t *testing.T) {
	for _, mode := range []string{"empty", "one", "many", "duplicate", "missing-id", "null-list", "missing-list", "short-list", "count-change", "count-missing", "count-null", "count-negative", "count-fraction", "count-string", "count-too-large", "count-unavailable", "list-unavailable", "partial-list", "bad-target"} {
		t.Run(mode, func(t *testing.T) {
			var counts, requests atomic.Int32
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				requests.Add(1)
				if r.Method != "GET" || r.URL.RawQuery != "" {
					t.Error("unpaged reader attempted mutation or undocumented paging")
				}
				if strings.HasSuffix(r.URL.Path, "/count") {
					seq := counts.Add(1)
					if mode == "count-unavailable" {
						w.WriteHeader(404)
						return
					}
					switch mode {
					case "count-missing":
						fmt.Fprint(w, `{"response":{}}`)
						return
					case "count-null":
						fmt.Fprint(w, `{"response":{"count":null}}`)
						return
					case "count-fraction":
						fmt.Fprint(w, `{"response":{"count":1.5}}`)
						return
					case "count-string":
						fmt.Fprint(w, `{"response":{"count":"1"}}`)
						return
					}
					n := 1
					switch mode {
					case "empty":
						n = 0
					case "many":
						n = 600
					case "duplicate":
						n = 2
					case "count-negative":
						n = -1
					case "count-too-large":
						n = 10001
					case "count-change":
						if seq == 2 {
							n = 2
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"count": n}})
					return
				}
				if mode == "list-unavailable" {
					w.WriteHeader(503)
					return
				}
				if mode == "partial-list" {
					w.WriteHeader(206)
					return
				}
				switch mode {
				case "null-list":
					fmt.Fprint(w, `{"response":null}`)
					return
				case "missing-list":
					fmt.Fprint(w, `{}`)
					return
				}
				rows := []map[string]any{{"id": "profile", "ignored": "private profile value"}}
				switch mode {
				case "empty", "short-list":
					rows = []map[string]any{}
				case "many":
					rows = []map[string]any{}
					for i := 0; i < 600; i++ {
						rows = append(rows, map[string]any{"id": fmt.Sprintf("profile-%d", i)})
					}
				case "duplicate":
					rows = append(rows, rows[0])
				case "missing-id":
					rows = []map[string]any{{"name": "private profile name"}}
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			target := "template"
			if mode == "bad-target" {
				target = "../foreign"
			}
			got, err := c.templateAttachedProfiles(context.Background(), target)
			if mode == "empty" || mode == "one" || mode == "many" {
				want := 1
				if mode == "empty" {
					want = 0
				}
				if mode == "many" {
					want = 600
				}
				if err != nil || len(got) != want || counts.Load() != 2 {
					t.Fatal("complete reference view refused", err, len(got))
				}
			} else if err == nil || got != nil {
				t.Fatal("incomplete reference view acquired absence authority")
			}
			if mode == "bad-target" && requests.Load() != 0 {
				t.Fatal("invalid identity reached native API")
			}
		})
	}
}

func TestTemplateProfileReferenceViews(t *testing.T) {
	for _, mode := range []string{"empty", "dayn", "onboarding", "modern-only", "legacy-only", "unknown-attribute", "unknown-member", "bad-member", "duplicate-group", "duplicate-member", "missing-detail", "renamed-detail", "summary-error", "late-modern-same-count", "late-legacy-reference", "unrelated-settings"} {
		t.Run(mode, func(t *testing.T) {
			var lists atomic.Int32
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("reference observer mutated native state")
				}
				root := templateReferenceProfilesRoot + "template/networkProfilesForSites"
				switch r.URL.Path {
				case root + "/count":
					n := 0
					if mode == "dayn" || mode == "modern-only" || mode == "late-modern-same-count" {
						n = 1
					}
					fmt.Fprintf(w, `{"response":{"count":%d}}`, n)
				case root:
					read := lists.Add(1)
					rows := []map[string]any{}
					if mode == "dayn" || mode == "modern-only" || mode == "late-modern-same-count" {
						id := "profile"
						if mode == "late-modern-same-count" && read > 1 {
							id = "replacement"
						}
						rows = append(rows, map[string]any{"id": id})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case networkProfilesPath:
					if mode == "summary-error" {
						w.WriteHeader(503)
						return
					}
					rows := []networkProfile{}
					if mode != "empty" && mode != "modern-only" && mode != "late-modern-same-count" {
						rows = append(rows, networkProfile{ID: "profile", Name: "private profile name", Namespace: "switching"})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case networkProfilesPath + "/profile":
					if mode == "missing-detail" {
						w.WriteHeader(404)
						return
					}
					p := networkProfile{ID: "profile", Name: "private profile name", Namespace: "switching", Attributes: []networkProfileAttribute{}}
					key := "cli.templates"
					if mode == "onboarding" {
						key = "day0.templates"
					}
					value := "template"
					if mode == "unrelated-settings" || mode == "late-legacy-reference" && lists.Load() == 1 {
						value = "unrelated"
					}
					a := networkProfileAttribute{Key: key, Attributes: []networkProfileTemplate{{Key: "template.id", Value: value}}}
					if mode == "unknown-attribute" {
						a.Key = "unqualified.template.class"
					}
					if mode == "unknown-member" {
						a.Attributes[0].Key = "unknown.ref"
					}
					if mode == "bad-member" {
						a.Attributes[0].Value = "../foreign"
					}
					if mode == "duplicate-member" {
						a.Attributes = append(a.Attributes, a.Attributes[0])
					}
					p.Attributes = append(p.Attributes, a)
					if mode == "duplicate-group" {
						p.Attributes = append(p.Attributes, a)
					}
					if mode == "renamed-detail" {
						p.Name = "replacement"
					}
					if mode == "unrelated-settings" {
						p.Attributes = append(p.Attributes, networkProfileAttribute{Key: "site.settings", raw: json.RawMessage(`{"key":"site.settings","private":"never expose or hash"}`)})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": p})
				default:
					t.Error("unexpected reference path")
					w.WriteHeader(404)
				}
			})
			got, err := c.stableTemplateProfileReferences(context.Background(), "template")
			valid := mode == "empty" || mode == "dayn" || mode == "onboarding" || mode == "modern-only" || mode == "legacy-only" || mode == "unrelated-settings"
			if valid {
				want := 1
				if mode == "empty" || mode == "unrelated-settings" {
					want = 0
				}
				if mode == "dayn" {
					want = 2
				}
				if err != nil || len(got) != want {
					t.Fatal("complete profile references refused", err, len(got))
				}
				raw, _ := json.Marshal(got)
				if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "never expose") {
					t.Fatal("opaque profile state disclosed")
				}
			} else if err == nil || got != nil {
				t.Fatal("uncertain/changed profile view became a stable reference set")
			}
		})
	}
}
