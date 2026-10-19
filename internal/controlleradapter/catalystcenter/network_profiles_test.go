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

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestNetworkProfileIntentValidation(t *testing.T) {
	for _, entry := range []string{
		`{"switching":[{"name":"p","unknown":true}]}`,
		`{"wireless":[]}`, `{"switching":null}`,
		`{"switching":[{"name":"p"},{"name":"p"}]}`,
		`{"switching":[{"name":"p","sites":["Global"]}]}`,
		`{"switching":[{"name":"p","dayn_templates":["p##t"]}]}`,
		`{"switching":[{"name":"p","dayn_templates":["t","t"]}]}`,
		`{"switching":[{"name":"p","onboarding_templates":null}]}`,
	} {
		if _, err := parseNetworkProfiles([]byte(`{"catalyst_center":{"network_profiles":` + entry + `}}`)); err == nil {
			t.Fatalf("accepted invalid intent: %s", entry)
		}
	}
	// Explicit empty selection supports retained association pruning; null is
	// still invalid and cannot erase an unresolved collection.
	if empty, err := parseNetworkProfiles([]byte(`{"catalyst_center":{"network_profiles":{"switching":[]}}}`)); err != nil || len(empty) != 0 {
		t.Fatal("explicit empty selection rejected", err)
	}
	want, err := parseNetworkProfiles([]byte(`{"catalyst_center":{"network_profiles":{"switching":[{"name":"p","dayn_templates":["project#dayn","other"],"onboarding_templates":["onboard"],"sites":["Global/Lab"]}]}}}`))
	if err != nil || len(want) != 1 || len(want[0].dayn) != 2 || len(want[0].sites) != 1 {
		t.Fatal(want, err)
	}
}

func TestNetworkProfileTemplateResolution(t *testing.T) {
	projects := map[string]templateProject{"a": {ID: "a"}, "b": {ID: "b"}}
	definitions := map[string]templateSummary{"a#t": {ID: "ta", ProjectID: "a", Name: "t"}, "b#t": {ID: "tb", ProjectID: "b", Name: "t"}}
	if _, err := resolveProfileTemplate("t", projects, definitions); err == nil {
		t.Fatal("ambiguous bare reference accepted")
	}
	if d, err := resolveProfileTemplate("a#t", projects, definitions); err != nil || d.ID != "ta" {
		t.Fatal(d, err)
	}
	delete(definitions, "b#t")
	if _, err := resolvedSwitchingProfile(switchingProfileIntent{name: "p", dayn: []string{"t", "a#t"}}, projects, definitions); err == nil {
		t.Fatal("duplicate resolved aliases accepted")
	}
	if _, err := resolveProfileTemplate("b#t", projects, definitions); err == nil {
		t.Fatal("missing dependency accepted")
	}
}

func TestNetworkProfileExecutionAndIdentity(t *testing.T) {
	for _, mode := range []string{"create", "update", "missing-receipt", "missing-later-dependency", "replacement", "template-replacement", "unknown-attributes", "assigned-update"} {
		t.Run(mode, func(t *testing.T) {
			actual := networkProfile{}
			if mode != "create" && mode != "missing-later-dependency" {
				actual = networkProfile{ID: "profile", Name: "p", Namespace: "switching", Attributes: []networkProfileAttribute{{Key: "cli.templates"}, {Key: "day0.templates"}}}
			}
			if mode == "unknown-attributes" {
				actual.Attributes = []networkProfileAttribute{{Key: "unmanaged"}}
			}
			if mode == "assigned-update" {
				var inherited networkProfileAttribute
				if err := json.Unmarshal([]byte(`{"key":"site.settings","attribs":[{"key":"site.uuid","value":"site","attribs":[{"key":"opaque-setting","value":"fixture-only-value"}]}]}`), &inherited); err != nil {
					t.Fatal(err)
				}
				actual.Attributes = append(actual.Attributes, inherited)
			}
			templateID := "template"
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case templateProjectsPath:
					fmt.Fprint(w, `[{"id":"project","name":"Project"}]`)
				case templatesPath:
					json.NewEncoder(w).Encode([]templateSummary{{ID: templateID, ProjectID: "project", Name: "t"}})
				case networkProfilesPath, networkProfilesPath + "/profile", networkProfilesPath + "/replacement":
					if r.Method == "GET" {
						if r.URL.Path == networkProfilesPath {
							profiles := []networkProfile{{ID: "foreign", Name: "p", Namespace: "authentication"}}
							if actual.ID != "" {
								profiles = append(profiles, actual)
							}
							json.NewEncoder(w).Encode(map[string]any{"response": profiles})
						} else {
							if r.URL.Query().Get("populated") != "true" {
								t.Error("profile details require populated=true")
							}
							json.NewEncoder(w).Encode(map[string]any{"response": actual})
						}
						return
					}
					expected := "POST"
					if mode != "create" {
						expected = "PUT"
					}
					if r.Method != expected {
						t.Errorf("method %s want %s", r.Method, expected)
					}
					var p networkProfile
					if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
						t.Error(err)
					}
					expectedAttributes := 2
					if mode == "assigned-update" {
						expectedAttributes = 3
						raw, _ := json.Marshal(p.Attributes[2])
						if !strings.Contains(string(raw), "fixture-only-value") {
							t.Error("server-owned site settings lost")
						}
					}
					if p.ID != "" || p.Namespace != "switching" || len(p.Attributes) != expectedAttributes || p.Attributes[0].Attributes[0].Value != "template" {
						t.Error("wrong profile payload")
					}
					p.ID = "profile"
					actual = p
					writes++
					if mode == "missing-receipt" {
						fmt.Fprint(w, `{}`)
					} else {
						fmt.Fprint(w, `{"response":{"taskId":"profile-task"}}`)
					}
				default:
					t.Errorf("unexpected API %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			model := `{"catalyst_center":{"network_profiles":{"switching":[{"name":"p","dayn_templates":["Project#t"]}]}}}`
			if mode == "missing-later-dependency" {
				model = strings.Replace(model, `["Project#t"]}]`, `["Project#t"]},{"name":"later","dayn_templates":["missing"]}]`, 1)
			}
			executor, err := c.compileConfigurationExecutor([]byte(model), "network_profiles")
			if err != nil {
				t.Fatal(err)
			}
			write, err := executor.next(context.Background())
			if mode == "missing-later-dependency" || mode == "unknown-attributes" {
				if err == nil || write != nil || writes != 0 {
					t.Fatal("unsafe preflight", err)
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal("no plan", err)
			}
			_, err = write.submit(context.Background())
			if mode == "missing-receipt" {
				if err == nil || writes != 1 {
					t.Fatal("uncertain receipt accepted or replayed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			executor, err = c.compileConfigurationExecutor([]byte(model), "network_profiles")
			if err != nil {
				t.Fatal(err)
			}
			op := &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent}
			if mode == "replacement" {
				actual.ID = "replacement"
			}
			if mode == "template-replacement" {
				templateID = "replacement"
			}
			ok, err := executor.verify(context.Background(), op)
			if mode == "replacement" || mode == "template-replacement" {
				if err == nil || ok {
					t.Fatal("replacement accepted")
				}
				return
			}
			if err != nil || !ok {
				t.Fatal("verification", err)
			}
			if next, err := executor.next(context.Background()); err != nil || next != nil {
				t.Fatal("not converged", err)
			}
			if writes != 1 {
				t.Fatal("replayed")
			}
		})
	}
}

func TestNetworkProfileEmptyGroups(t *testing.T) {
	want := networkProfile{Name: "p", Namespace: "switching", Attributes: []networkProfileAttribute{{Key: "cli.templates"}, {Key: "day0.templates"}}}
	got := networkProfile{Name: "p", Namespace: "switching"}
	if !switchingProfileMatches(want, got) {
		t.Fatal("API omission of empty groups causes perpetual drift")
	}
}

func TestNetworkProfileSiteAssignment(t *testing.T) {
	for _, mode := range []string{"assign", "missing-site", "replacement-site", "replacement-profile"} {
		t.Run(mode, func(t *testing.T) {
			profileID, siteID := "profile", "site"
			assigned := []map[string]string{{"id": "foreign-site"}}
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == networkProfilesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []networkProfile{{ID: profileID, Name: "p", Namespace: "switching"}}})
				case r.URL.Path == sitesPath:
					sites := []Site{{ID: "root", Name: "Global", NameHierarchy: "Global", Type: "global"}}
					if mode != "missing-site" {
						sites = append(sites, Site{ID: siteID, Name: "Lab", NameHierarchy: "Global/Lab", ParentID: "root", Type: "area"})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": sites})
				case r.URL.Path == profileSiteAssignmentsPath(profileID):
					if r.Method == "GET" {
						json.NewEncoder(w).Encode(map[string]any{"response": assigned})
						return
					}
					if r.Method != "POST" {
						t.Error("must be additive POST")
					}
					var body map[string]string
					json.NewDecoder(r.Body).Decode(&body)
					if len(body) != 1 || body["id"] != "site" {
						t.Error("incorrect assignment payload")
					}
					assigned = append(assigned, body)
					writes++
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			executor := &profileSiteExecutor{client: c, desired: []profileSiteIntent{{name: "p", sites: []string{"Global/Lab"}}}}
			write, err := executor.next(context.Background())
			if mode == "missing-site" {
				if err == nil || write != nil {
					t.Fatal("missing site accepted")
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal(err)
			}
			if _, err = write.submit(context.Background()); err != nil {
				t.Fatal(err)
			}
			op := &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent}
			if mode == "replacement-site" {
				siteID = "replaced"
			}
			if mode == "replacement-profile" {
				profileID = "replaced"
			}
			ok, err := executor.verify(context.Background(), op)
			if mode != "assign" {
				if err == nil || ok {
					t.Fatal("replacement accepted")
				}
				return
			}
			if err != nil || !ok {
				t.Fatal(err)
			}
			if next, err := executor.next(context.Background()); err != nil || next != nil {
				t.Fatal("not converged", err)
			}
			if writes != 1 || len(assigned) != 2 || assigned[0]["id"] != "foreign-site" {
				t.Fatal("foreign association lost or replayed")
			}
		})
	}
}

func TestNetworkProfileMissingSiteBlocksEarlierCreate(t *testing.T) {
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.URL.Path != authPath {
			writes++
			t.Error("mutation before all dependencies observed")
		}
		switch r.URL.Path {
		case authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case networkProfilesPath:
			fmt.Fprint(w, `{"response":[]}`)
		case sitesPath:
			fmt.Fprint(w, `{"response":[{"id":"root","name":"Global","nameHierarchy":"Global","type":"global"}]}`)
		default:
			t.Error("unexpected API", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	model := []byte(`{"catalyst_center":{"network_profiles":{"switching":[{"name":"first"},{"name":"later","sites":["Global/Missing"]}]}}}`)
	executor, err := c.compileConfigurationExecutor(model, "network_profiles")
	if err != nil {
		t.Fatal(err)
	}
	if write, err := executor.next(context.Background()); err == nil || write != nil || writes != 0 {
		t.Fatal("missing site allowed earlier profile create")
	}
}

func TestNetworkProfilePopulatedReadIsRequired(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.URL.Query().Get("populated") != "true" {
			t.Error("missing populated query")
		}
		fmt.Fprint(w, `{"response":{"siteProfileUuid":"profile","name":"p","namespace":"switching"}}`)
	})
	_, err := c.readSwitchingProfile(context.Background(), networkProfile{ID: "profile", Name: "p", Namespace: "switching"})
	if err == nil {
		t.Fatal("metadata-only observation accepted as empty configuration")
	}
}

func TestNetworkProfileSiteSettingsExcludedFromDependencyHash(t *testing.T) {
	p := networkProfile{Name: "p", Namespace: "switching", Attributes: []networkProfileAttribute{{Key: "cli.templates"}, {Key: "day0.templates"}}}
	before := profileDependencyIdentity(p)
	var settings networkProfileAttribute
	if err := json.Unmarshal([]byte(`{"key":"site.settings","attribs":[{"key":"opaque","value":"fixture-only-value"}]}`), &settings); err != nil {
		t.Fatal(err)
	}
	p.Attributes = append(p.Attributes, settings)
	if profileDependencyIdentity(p) != before {
		t.Fatal("inherited settings enter operation dependency hash")
	}
	if !switchingProfileMatches(p, networkProfile{Name: "p", Namespace: "switching"}) {
		t.Fatal("server settings cause drift")
	}
}
