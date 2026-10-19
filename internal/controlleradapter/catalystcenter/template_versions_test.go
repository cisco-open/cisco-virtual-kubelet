// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestLatestTemplateVersion(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		valid            bool
	}{
		{"numeric", `[{"templateId":"base","versionsInfo":[{"id":"draft","version":null},{"id":"two","version":"2"},{"id":"ten","version":10}]}]`, "ten", true},
		{"omitted-base-draft", `[{"templateId":"base","versionsInfo":[{"id":"base"}]}]`, "", true},
		{"draft", `[{"templateId":"base","versionsInfo":[{"id":"draft","version":0}]}]`, "", true},
		{"missing", `[]`, "", true},
		{"wrong-parent", `[{"templateId":"other","versionsInfo":[]}]`, "", false},
		{"duplicate", `[{"templateId":"base","versionsInfo":[{"id":"a","version":1},{"id":"b","version":"1"}]}]`, "", false},
		{"fraction", `[{"templateId":"base","versionsInfo":[{"id":"a","version":1.5}]}]`, "", false},
		{"absent-version", `[{"templateId":"base","versionsInfo":[{"id":"a"}]}]`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != templateVersionsPath+"/base" || r.Method != "GET" {
					t.Error("unexpected request")
				}
				fmt.Fprint(w, tc.body)
			})
			got, err := c.latestTemplateVersion(context.Background(), "base")
			if (err == nil) != tc.valid {
				t.Fatal("invalid version acceptance", err)
			}
			if !tc.valid {
				return
			}
			if tc.want == "" {
				if got != nil {
					t.Fatal("draft considered committed")
				}
			} else if got == nil || got.ID != tc.want {
				t.Fatal("wrong latest version")
			}
		})
	}
}

func TestCommittedTemplateIdentity(t *testing.T) {
	for _, mode := range []string{"match", "base-id", "project", "version", "name"} {
		t.Run(mode, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != templatesPath+"/committed" || r.URL.RawQuery != "" {
					t.Error("committed read used draft query")
				}
				body := map[string]any{"id": "committed", "projectId": "project", "name": "template", "version": "2"}
				switch mode {
				case "base-id":
					body["id"] = "base"
				case "project":
					body["projectId"] = "other"
				case "version":
					body["version"] = "1"
				case "name":
					body["name"] = "other"
				}
				json.NewEncoder(w).Encode(body)
			})
			_, err := c.readCommittedTemplate(context.Background(), templateSummary{ID: "base", ProjectID: "project", Name: "template"}, templateVersion{ID: "committed", Number: 2})
			if (err == nil) != (mode == "match") {
				t.Fatal("incorrect committed identity acceptance", err)
			}
		})
	}
}
