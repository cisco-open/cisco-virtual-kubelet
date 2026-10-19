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

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestTemplateVariableChoiceResetComparison(t *testing.T) {
	for _, mode := range []string{"equal", "empty", "clear-retained", "remove-choice", "missing-choice", "changed-value", "native-metadata", "unmanaged-choices", "default-changed", "default-cleared", "default-retained"} {
		t.Run(mode, func(t *testing.T) {
			want := map[string]any{"parameterName": "value", "dataType": "STRING", "defaultValue": "one", "selection": map[string]any{"selectionType": "SINGLE_SELECT", "selectionValues": map[string]any{"first": "one"}}}
			got := map[string]any{"parameterName": "value", "dataType": "STRING", "defaultValue": "one", "selection": map[string]any{"selectionType": "SINGLE_SELECT", "selectionValues": map[string]any{"first": "one"}}}
			ws, gs := want["selection"].(map[string]any), got["selection"].(map[string]any)
			matches := true
			switch mode {
			case "empty":
				ws["selectionValues"], gs["selectionValues"] = map[string]any{}, map[string]any{}
			case "clear-retained":
				ws["selectionValues"] = map[string]any{}
				matches = false
			case "remove-choice":
				gs["selectionValues"].(map[string]any)["second"] = "two"
				matches = false
			case "missing-choice":
				gs["selectionValues"] = map[string]any{}
				matches = false
			case "changed-value":
				gs["selectionValues"].(map[string]any)["first"] = "two"
				matches = false
			case "native-metadata":
				got["id"] = "native-id"
				gs["defaultSelectedValues"] = []any{"one"}
			case "unmanaged-choices":
				delete(ws, "selectionValues")
			case "default-changed":
				want["defaultValue"] = "two"
				matches = false
			case "default-cleared":
				want["defaultValue"], got["defaultValue"] = "", ""
			case "default-retained":
				want["defaultValue"] = ""
				matches = false
			}
			if templateDefinitionMatches(map[string]any{"templateParams": []any{want}}, map[string]any{"templateParams": []any{got}}) != matches {
				t.Fatal("managed parameter clearing/comparison contract violated")
			}
		})
	}
}

func TestTemplateVariableResetReportPlanAndVerify(t *testing.T) {
	for _, mode := range []string{"cleared", "choices-retained", "default-retained", "choice-removed", "choice-value-changed", "committed-choices-retained"} {
		t.Run(mode, func(t *testing.T) {
			raw := strings.Replace(templateModel, "data_values: {first: value}", "default_value: \"\"\n                data_values: {}", 1)
			if mode == "choice-removed" || mode == "choice-value-changed" {
				raw = strings.Replace(raw, "data_values: {}", "data_values: {first: value}", 1)
			}
			files := map[string]string{"base.j2": "! {{ hostname }}\n"}
			intent, err := parseTemplateIntent([]byte(raw), files)
			if err != nil {
				t.Fatal(err)
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != http.MethodGet {
					t.Error("report/plan/verification submitted a mutation")
					w.WriteHeader(400)
					return
				}
				switch r.URL.Path {
				case templateProjectsPath:
					fmt.Fprint(w, `[{"id":"project","name":"Lab"}]`)
				case templatesPath:
					fmt.Fprint(w, `[{"templateId":"template","projectId":"project","name":"base"}]`)
				case templateVersionsPath + "/template":
					fmt.Fprint(w, `[{"templateId":"template","versionsInfo":[{"id":"template"},{"id":"committed","version":"1"}]}]`)
				case templatesPath + "/template", templatesPath + "/committed":
					// Clone through JSON so observation changes cannot edit desired intent.
					encoded, _ := json.Marshal(intent.definitions[0].payload)
					body := map[string]any{}
					json.Unmarshal(encoded, &body)
					body["id"], body["projectId"] = "template", "project"
					committed := r.URL.Path == templatesPath+"/committed"
					if committed {
						body["id"], body["version"] = "committed", "1"
					}
					param := body["templateParams"].([]any)[0].(map[string]any)
					selection := param["selection"].(map[string]any)
					switch mode {
					case "choices-retained":
						selection["selectionValues"] = map[string]any{"old": "private choice"}
					case "committed-choices-retained":
						if committed {
							selection["selectionValues"] = map[string]any{"old": "private choice"}
						}
					case "default-retained":
						param["defaultValue"] = "private default"
					case "choice-removed":
						selection["selectionValues"].(map[string]any)["old"] = "private choice"
					case "choice-value-changed":
						selection["selectionValues"].(map[string]any)["first"] = "private choice"
					}
					json.NewEncoder(w).Encode(body)
				default:
					w.WriteHeader(404)
				}
			})
			reports, err := c.compileConfigReports([]byte(raw), []string{"templates"}, files)
			if err != nil || len(reports) != 1 {
				t.Fatal("reset report compilation failed", err)
			}
			drift, err := reports[0].observe(context.Background())
			// Definition reports observe the draft. Apply independently checks the
			// committed body, so an uncommitted reset must still plan a commit.
			if err != nil || (len(drift) > 0) != (mode != "cleared" && mode != "committed-choices-retained") {
				t.Fatal("retained parameter values hidden from drift", err)
			}
			ex, err := c.compileConfigurationExecutor([]byte(raw), "templates", files)
			if err != nil {
				t.Fatal(err)
			}
			write, err := ex.next(context.Background())
			if err != nil || (write != nil) != (mode != "cleared") {
				t.Fatal("reset apply plan inconsistent", err)
			}
			if write == nil {
				return
			}
			prefix := "draft:"
			if mode == "committed-choices-retained" {
				prefix = "commit:"
			}
			if !strings.HasPrefix(write.resource, prefix) {
				t.Fatal("reset planned wrong stage")
			}
			ok, err := ex.verify(context.Background(), &cvk.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent})
			if err != nil || ok {
				t.Fatal("unapplied reset incorrectly verified", err)
			}
		})
	}
}
