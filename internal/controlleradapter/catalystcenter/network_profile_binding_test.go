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

func TestSwitchingProfileSubmissionGuards(t *testing.T) {
	for _, mode := range []string{"unknown-native-change", "stable", "project-replaced", "late-project-replaced", "profile-replaced", "later-profile-replaced", "template-replaced", "inherited-changed", "inherited-precision-changed", "inherited-reordered", "attribute-order", "opaque-value-order", "caller-mutated", "unrelated", "lost-ack"} {
		t.Run(mode, func(t *testing.T) {
			project, template, profile, later := "project", "template", "profile", "later"
			changed, reads, writes := false, 0, 0
			opaque := `{"key":"site.settings","attribs":[{"key":"private","value":"fixture-inherited-value"}]}`
			if mode == "attribute-order" {
				opaque = `{"key":"site.settings","attribs":[{"key":"a","value":"one"},{"key":"b","value":"two"}]}`
			}
			if mode == "opaque-value-order" {
				opaque = `{"key":"site.settings","attribs":[{"key":"private","value":"fixture-inherited-value"}],"opaqueList":["one","two"]}`
			}
			if mode == "inherited-precision-changed" {
				opaque = strings.TrimSuffix(opaque, "}") + `,"counter":9007199254740992}`
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != http.MethodGet {
					writes++
					var payload networkProfile
					if r.Method != http.MethodPut || r.URL.Path != networkProfilesPath+"/profile" || json.NewDecoder(r.Body).Decode(&payload) != nil || payload.Name != "p" {
						t.Error("profile destination changed")
					}
					if len(payload.Attributes) != 3 || payload.Attributes[0].Attributes[0].Value != "template" {
						t.Error("profile template or preserved group changed")
					}
					if mode == "lost-ack" {
						fmt.Fprint(w, `{}`)
					} else {
						fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					}
					return
				}
				switch r.URL.Path {
				case templateProjectsPath:
					_ = json.NewEncoder(w).Encode([]any{map[string]string{"id": project, "name": "Project"}})
				case templatesPath:
					_ = json.NewEncoder(w).Encode([]templateSummary{{ID: template, ProjectID: project, Name: "t"}})
				case networkProfilesPath:
					reads++
					rows := []networkProfile{{ID: profile, Name: "p", Namespace: "switching"}, {ID: later, Name: "later", Namespace: "switching"}}
					if changed && mode == "unrelated" {
						rows = append(rows, networkProfile{ID: "foreign", Name: "foreign", Namespace: "switching"})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
				default:
					name, id := "p", profile
					if strings.HasSuffix(r.URL.Path, "/"+later) {
						name, id = "later", later
					}
					if r.URL.Path != networkProfilesPath+"/"+id {
						t.Error("unexpected profile read")
						http.NotFound(w, r)
						return
					}
					var inherited networkProfileAttribute
					if err := json.Unmarshal([]byte(opaque), &inherited); err != nil {
						t.Error("bad private fixture")
					}
					if mode == "unknown-native-change" {
						extension := "fixture-original-private"
						if changed {
							extension = "fixture-changed-private"
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"siteProfileUuid": id, "name": name, "namespace": "switching", "profileAttributes": []networkProfileAttribute{{Key: "cli.templates"}, {Key: "day0.templates"}, inherited}, "extension": map[string]any{"value": extension}}})
					} else {
						_ = json.NewEncoder(w).Encode(map[string]any{"response": networkProfile{ID: id, Name: name, Namespace: "switching", Attributes: []networkProfileAttribute{{Key: "cli.templates"}, {Key: "day0.templates"}, inherited}}})
					}
					if changed && mode == "late-project-replaced" && reads == 2 {
						project = "replacement-project"
					}
				}
			})
			e := &switchingProfileExecutor{client: c, desired: []switchingProfileIntent{{name: "p", dayn: []string{"Project#t"}}, {name: "later", dayn: []string{"Project#t"}}}}
			write, err := e.next(context.Background())
			if err != nil || write == nil || !validSwitchingProfileBinding(write.identity.parent) {
				t.Fatal("missing bound profile plan", err)
			}
			changed = true
			switch mode {
			case "project-replaced":
				project = "replacement-project"
			case "profile-replaced":
				profile = "replacement-profile"
			case "later-profile-replaced":
				later = "replacement-later"
			case "template-replaced":
				template = "replacement-template"
			case "inherited-changed":
				opaque = strings.ReplaceAll(opaque, "fixture-inherited-value", "changed-private-value")
			case "inherited-precision-changed":
				opaque = strings.ReplaceAll(opaque, "9007199254740992", "9007199254740993")
			case "inherited-reordered":
				opaque = `{"attribs":[{"value":"fixture-inherited-value","key":"private"}],"key":"site.settings"}`
			case "attribute-order":
				opaque = `{"key":"site.settings","attribs":[{"key":"b","value":"two"},{"key":"a","value":"one"}]}`
			case "opaque-value-order":
				opaque = strings.Replace(opaque, `["one","two"]`, `["two","one"]`, 1)
			case "caller-mutated":
				e.desired[0].name = "caller-name"
				e.desired[0].dayn[0] = "missing"
			}
			_, err = write.submit(context.Background())
			allowed := mode == "stable" || mode == "inherited-reordered" || mode == "attribute-order" || mode == "caller-mutated" || mode == "unrelated"
			if allowed && (err != nil || writes != 1) {
				t.Fatal("stable profile refused", err, writes)
			}
			if !allowed && (err == nil || mode != "lost-ack" && writes != 0 || mode == "lost-ack" && writes != 1) {
				t.Fatal("stale or uncertain profile was accepted", writes)
			}
			if !allowed {
				proof := write.noDispatch(err)
				if (proof != nil) != (mode != "lost-ack") {
					t.Fatal("preflight proof confused with a dispatched request")
				}
				if proof != nil && write.noDispatch(err) != nil {
					t.Fatal("preflight proof claimed twice")
				}
			}
			if err != nil && (strings.Contains(err.Error(), "fixture-inherited-value") || strings.Contains(err.Error(), "changed-private-value")) {
				t.Fatal("inherited value exposed")
			}
			before := writes
			if _, err := write.submit(context.Background()); err == nil || writes != before {
				t.Fatal("profile write replayed")
			}
		})
	}
}

func TestSwitchingProfileProjectBindingSurvivesRestart(t *testing.T) {
	for _, mode := range []string{"stable", "project-replaced", "legacy", "legacy-empty"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			project := "original-project"
			actual := networkProfile{}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case templateProjectsPath:
					_ = json.NewEncoder(w).Encode([]any{map[string]string{"id": project, "name": "Project"}})
				case templatesPath:
					_ = json.NewEncoder(w).Encode([]templateSummary{{ID: "template", ProjectID: project, Name: "t"}})
				case networkProfilesPath:
					if r.Method == http.MethodGet {
						list := []networkProfile{}
						if actual.ID != "" {
							list = append(list, actual)
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"response": list})
						return
					}
					_ = json.NewDecoder(r.Body).Decode(&actual)
					actual.ID = "profile"
					fmt.Fprint(w, `{"response":{"taskId":"profile-task"}}`)
				case networkProfilesPath + "/profile":
					_ = json.NewEncoder(w).Encode(map[string]any{"response": actual})
				default:
					t.Error("unexpected recovery request")
					http.NotFound(w, r)
				}
			})
			e := &switchingProfileExecutor{client: c, desired: []switchingProfileIntent{{name: "p", dayn: []string{"Project#t"}}}}
			if mode == "legacy-empty" {
				e.desired[0].dayn = nil
			}
			write, err := e.next(ctx)
			if err != nil || write == nil {
				t.Fatal(err)
			}
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"network_profiles"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			op, err := a.prepareConfiguration(ctx, config, nil, "network_profiles", "profiles/"+write.resource, write.action, write.identity)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.startConfiguration(ctx, config, nil, op.ID, write.identity); err != nil {
				t.Fatal(err)
			}
			task, err := write.submit(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.advanceConfiguration(ctx, op.ID, "Accepted", task.ID); err != nil {
				t.Fatal(err)
			}
			stored, _ := json.Marshal(journalState(t, a).Operation)
			var recovered cvk.NetworkControllerConfigurationOperation
			if err := json.Unmarshal(stored, &recovered); err != nil {
				t.Fatal(err)
			}
			if mode == "project-replaced" {
				project = "replacement-project"
			}
			if mode == "legacy" {
				recovered.ParentID = strings.Repeat("a", 64)
			}
			if mode == "legacy-empty" {
				recovered.ParentID = profileDependencyIdentity(networkProfile{})
			}
			recovered.Resource = strings.TrimPrefix(recovered.Resource, "profiles/")
			fresh := &switchingProfileExecutor{client: c, desired: e.desired}
			ok, err := fresh.verify(ctx, &recovered)
			valid := mode == "stable" || mode == "legacy-empty"
			if valid && (err != nil || !ok) || !valid && (err == nil || ok) {
				t.Fatal("wrong recovered project result", err)
			}
		})
	}
}

func TestSwitchingProfileLegacyVerifiedBoundary(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprintf("empty=%t", empty), func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"network_profiles"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			binding := strings.Repeat("a", 64)
			if empty {
				binding = profileDependencyIdentity(networkProfile{})
			}
			identity := configurationResourceIdentity{parent: binding}
			op, err := a.prepareConfiguration(ctx, config, nil, "network_profiles", "profiles/p", "Create", identity)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.startConfiguration(ctx, config, nil, op.ID, identity); err != nil {
				t.Fatal(err)
			}
			for _, phase := range []string{"Accepted", "Verifying", "Verified"} {
				task := ""
				if phase == "Accepted" {
					task = "task"
				}
				if err := a.advanceConfiguration(ctx, op.ID, phase, task); err != nil {
					t.Fatal(err)
				}
			}
			err = a.finishConfiguration(ctx, op.ID)
			if empty && err != nil || !empty && err != errSwitchingProfileBinding {
				t.Fatal("legacy profile release did not honor original empty-set evidence", err)
			}

		})
	}
}
