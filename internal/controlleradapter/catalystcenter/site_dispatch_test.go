// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func siteDispatchFixture(stage string) ([]desiredSite, []Site) {
	actual := sampleSites()
	address, country := "private original address", "Germany"
	actual[2].Address, actual[2].Country = &address, &country
	kind, action, _ := strings.Cut(stage, "-")
	path := map[string]string{"area": "Global/Probe", "building": "Global/Lab/Probe", "floor": "Global/Lab/Building/Probe"}[kind]
	fields := map[string]any{}
	if kind == "building" {
		fields["address"] = "private desired address"
	}
	if kind == "floor" {
		fields = map[string]any{"floor_number": float64(1), "rf_model": "Free Space", "width": float64(12), "length": float64(15), "height": float64(3), "units_of_measure": "meters"}
	}
	if action == "update" {
		if kind == "building" {
			path = "Global/Lab/Building"
		} else {
			path = "Global/Lab/Building/Floor"
			number, width, length, height, rf, units := float64(1), float64(10), float64(15), float64(3), "Free Space", "meters"
			actual = append(actual, Site{ID: "floor", Name: "Floor", NameHierarchy: path, ParentID: "building", Type: "floor", FloorNumber: &number, Width: &width, Length: &length, Height: &height, RFModel: &rf, UnitsOfMeasure: &units})
		}
	}
	return []desiredSite{{kind: kind, path: path, fields: fields}}, actual
}

func TestSiteSingularDispatch(t *testing.T) {
	for _, stage := range []string{"area-create", "building-create", "floor-create", "building-update", "floor-update"} {
		modes := []string{"stable", "parent-replaced", "ancestor-changed", "caller-intent", "caller-descendant", "during-guard", "guard-denied", "partial", "missing-root", "unrelated", "reordered", "uncertain", "target-changed"}
		if strings.HasSuffix(stage, "update") {
			modes = append(modes, "target-field", "unselected-field")
		}
		for _, mode := range modes {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				desired, original := siteDispatchFixture(stage)
				late, during := false, false
				posts, guards := 0, 0
				parent := parentSitePath(desired[0].path)
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == authPath {
						fmt.Fprint(w, `{"Token":"test"}`)
						return
					}
					if r.Method != "GET" {
						posts++
						if mode == "uncertain" {
							w.WriteHeader(503)
							return
						}
						var body map[string]any
						if json.NewDecoder(r.Body).Decode(&body) != nil {
							t.Error("invalid site payload")
						}
						for _, s := range original {
							if s.NameHierarchy == parent && body["parentId"] != s.ID {
								t.Error("submission lost captured parent")
							}
						}
						fmt.Fprint(w, `{"response":{"taskId":"site-task"}}`)
						return
					}
					if r.URL.Path != sitesPath {
						t.Error("unexpected dispatch observation")
						w.WriteHeader(500)
						return
					}
					encoded, _ := json.Marshal(original)
					var actual []Site
					json.Unmarshal(encoded, &actual)
					if late {
						switch mode {
						case "target-field", "unselected-field":
							for i := range actual {
								if actual[i].NameHierarchy != desired[0].path {
									continue
								}
								value := "private external edit"
								if mode == "unselected-field" {
									actual[i].Country = &value
								} else if actual[i].Type == "building" {
									actual[i].Address = &value
								} else {
									width := float64(99)
									actual[i].Width = &width
								}
							}
						case "partial":
							w.WriteHeader(206)
						case "missing-root":
							actual = actual[1:]
						case "unrelated":
							actual = append(actual, Site{ID: "other", Name: "Other", NameHierarchy: "Global/Other", ParentID: actual[0].ID, Type: "area"})
						case "reordered":
							for i, j := 0, len(actual)-1; i < j; i, j = i+1, j-1 {
								actual[i], actual[j] = actual[j], actual[i]
							}
						case "ancestor-changed":
							value := "private changed country"
							actual[0].Country = &value
						case "parent-replaced":
							old := ""
							for i := range actual {
								if actual[i].NameHierarchy == parent {
									old, actual[i].ID = actual[i].ID, "replacement-parent"
								}
							}
							for i := range actual {
								if actual[i].ParentID == old {
									actual[i].ParentID = "replacement-parent"
								}
							}
						case "target-changed":
							found := false
							for i := range actual {
								if actual[i].NameHierarchy == desired[0].path {
									actual[i].ID, found = "replacement-target", true
								}
							}
							if !found {
								for _, s := range actual {
									if s.NameHierarchy == parent {
										actual = append(actual, Site{ID: "appeared-target", Name: "Probe", NameHierarchy: desired[0].path, ParentID: s.ID, Type: desired[0].kind})
										break
									}
								}
							}
						}
					}
					if during {
						value := "private change during authority check"
						actual[0].Country = &value
					}
					json.NewEncoder(w).Encode(map[string]any{"response": actual})
				})
				ex := &siteConfigurationExecutor{client: c, desired: desired, submissionGuard: func(context.Context, string, string, configurationResourceIdentity) error {
					guards++
					if mode == "during-guard" {
						during = true
					}
					if mode == "guard-denied" {
						return errConfigurationOwned
					}
					return nil
				}}
				write, err := ex.next(context.Background())
				if err != nil || write == nil {
					t.Fatal("initial site plan failed", err)
				}
				late = true
				if mode == "caller-intent" {
					desired[0].fields["address"] = "private caller change"
				}
				if mode == "caller-descendant" {
					ex.desired = append(ex.desired, desiredSite{kind: "area", path: "Global/Later", fields: map[string]any{}})
				}
				task, err := write.submit(context.Background())
				accepted := mode == "stable" || mode == "unrelated" || mode == "reordered"
				if accepted {
					if err != nil || task.ID != "site-task" || posts != 1 || guards != 1 {
						t.Fatal("stable scoped site plan refused", err)
					}
				} else if mode == "uncertain" {
					if err == nil || posts != 1 || write.noDispatch(err) != nil {
						t.Fatal("uncertain request acquired no-dispatch authority")
					}
				} else if err == nil || posts != 0 || write.noDispatch == nil || write.noDispatch(err) == nil {
					t.Fatal("changed site plan was not refused with original guard proof")
				}
				before := posts
				if _, err := write.submit(context.Background()); !errors.Is(err, errConfigurationTransition) || posts != before {
					t.Fatal("consumed site write replayed")
				}
			})
		}
	}
}
