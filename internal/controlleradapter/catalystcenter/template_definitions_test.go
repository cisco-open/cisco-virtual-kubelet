// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const templateModel = `catalyst_center:
  templates:
    projects:
      - name: Lab
        dayn_templates:
          - name: base
            language: JINJA
            software_type: IOS-XE
            device_types: [{product_family: Switches and Hubs}]
            variables:
              - name: hostname
                data_type: STRING
                required: true
                selection_type: SINGLE_SELECT
                data_values: {first: value}
`

func TestTemplateDefinitionReport(t *testing.T) {
	for _, mode := range []string{"match", "changed", "missing", "replaced", "partial"} {
		t.Run(mode, func(t *testing.T) {
			files := map[string]string{"base.j2": "hostname {{ hostname }}\r\n"}
			intent, err := parseTemplateIntent([]byte(templateModel), files)
			if err != nil {
				t.Fatal(err)
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("report wrote API")
				}
				switch r.URL.Path {
				case templateProjectsPath:
					fmt.Fprint(w, `[{"id":"project","name":"Lab"}]`)
				case templatesPath:
					if r.URL.Query().Get("unCommitted") != "true" {
						t.Error("draft query missing")
					}
					if mode == "missing" {
						fmt.Fprint(w, `[]`)
					} else {
						fmt.Fprint(w, `[{"templateId":"template","projectId":"project","name":"base"}]`)
					}
				case templatesPath + "/template":
					body := map[string]any{}
					for k, v := range intent.definitions[0].payload {
						body[k] = v
					}
					body["id"] = "template"
					body["projectId"] = "project"
					if mode == "changed" {
						body["templateContent"] = "SECRET-NEVER-PRINT"
					}
					if mode == "replaced" {
						body["projectId"] = "foreign"
					}
					if mode == "partial" {
						w.WriteHeader(206)
					}
					json.NewEncoder(w).Encode(body)
				default:
					w.WriteHeader(404)
				}
			})
			plans, err := c.compileConfigReports([]byte(templateModel), []string{"templates"}, files)
			if err != nil {
				t.Fatal(err)
			}
			drift, err := plans[0].observe(context.Background())
			if mode == "replaced" || mode == "partial" {
				if err == nil || len(drift) != 0 {
					t.Fatal("invalid observation published")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := 1
			if mode == "match" {
				expected = 0
			}
			if len(drift) != expected {
				t.Fatalf("drift=%v", drift)
			}
			encoded, _ := json.Marshal(drift)
			if strings.Contains(string(encoded), "SECRET") || strings.Contains(string(encoded), "hostname") {
				t.Fatal("template values leaked")
			}
		})
	}
}
func TestTemplateDefinitionRejectsInvalidInput(t *testing.T) {
	for _, model := range []string{strings.Replace(templateModel, "language: JINJA", "language: OTHER", 1), strings.Replace(templateModel, "required: true", "required: yes", 1), strings.Replace(templateModel, "data_values: {first: value}", "unsupported: value", 1), strings.Replace(templateModel, "product_family:", "unknown:", 1)} {
		if _, err := parseTemplateIntent([]byte(model), map[string]string{"base.j2": ""}); err == nil {
			t.Fatal("invalid template definition accepted")
		}
	}
	if _, err := parseTemplateIntent([]byte(templateModel), nil); err == nil {
		t.Fatal("missing content accepted")
	}
}

func TestTemplateDefinitionCompositeAndSetComparison(t *testing.T) {
	raw := templateModel + `          - name: combined
            composite: true
            containing_templates: [base]
            language: JINJA
            software_type: IOS-XE
            device_types: [{product_family: Switches and Hubs}]
`
	intent, err := parseTemplateIntent([]byte(raw), map[string]string{"base.j2": "body"})
	if err != nil {
		t.Fatal(err)
	}
	if len(intent.definitions) != 2 || intent.definitions[1].identity.Members[0] != "Lab#base" {
		t.Fatal("composite dependency lost")
	}
	if _, exists := intent.definitions[1].payload["templateContent"]; exists {
		t.Fatal("composite contains regular body")
	}
	want := map[string]any{"deviceTypes": []any{map[string]any{"productFamily": "A"}, map[string]any{"productFamily": "B"}}}
	got := map[string]any{"deviceTypes": []any{map[string]any{"productFamily": "B", "extra": "metadata"}, map[string]any{"productFamily": "A"}}}
	if !templateDefinitionMatches(want, got) {
		t.Fatal("device set ordering produced drift")
	}
	got["deviceTypes"] = []any{map[string]any{"productFamily": "A"}, map[string]any{"productFamily": "A"}}
	if templateDefinitionMatches(want, got) {
		t.Fatal("duplicate observed item consumed twice")
	}
}
