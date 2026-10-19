// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestWirelessTagScopeRelationships(t *testing.T) {
	sites := map[string]string{"Global": "global", "Global/A": "a", "Global/A/Child": "child", "Global/AB": "ab", "Global/B": "b"}
	for _, test := range []struct {
		name          string
		policy        bool
		a, b, az, bz  []string
		conflict, bad bool
	}{
		{name: "site same", a: []string{"a"}, b: []string{"a"}, conflict: true},
		{name: "site parent distinct", a: []string{"a"}, b: []string{"child"}},
		{name: "site siblings", a: []string{"a"}, b: []string{"b"}},
		{name: "policy same zone", policy: true, a: []string{"a"}, b: []string{"a"}, az: []string{"Z"}, bz: []string{"Z"}, conflict: true},
		{name: "policy disjoint zones", policy: true, a: []string{"a"}, b: []string{"a"}, az: []string{"Z"}, bz: []string{"Other"}},
		{name: "policy partial zone overlap", policy: true, a: []string{"a"}, b: []string{"a"}, az: []string{"Z", "Other"}, bz: []string{"Other", "Third"}, conflict: true},
		{name: "policy parent", policy: true, a: []string{"a"}, b: []string{"child"}, conflict: true},
		{name: "policy child", policy: true, a: []string{"child"}, b: []string{"a"}, conflict: true},
		{name: "policy Global", policy: true, a: []string{"global"}, b: []string{"child"}, conflict: true},
		{name: "policy unrelated prefix", policy: true, a: []string{"a"}, b: []string{"ab"}},
		{name: "policy parent disjoint", policy: true, a: []string{"a"}, b: []string{"child"}, az: []string{"Z"}, bz: []string{"Other"}},
		{name: "policy default explicit versus empty", policy: true, a: []string{"a"}, b: []string{"a"}, az: []string{"default-zone"}, conflict: true},
		{name: "unknown site", policy: true, a: []string{"a"}, b: []string{"missing"}, bad: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := map[string]any{"siteIds": test.a, "apZones": test.az}
			b := map[string]any{"siteIds": test.b, "apZones": test.bz}
			got, err := wirelessTagScopesConflict(a, b, sites, test.policy)
			if (err != nil) != test.bad || !test.bad && got != test.conflict {
				t.Fatal("wrong scope relationship", got, err)
			}
		})
	}
}

func TestWirelessTagScopeWholeModelAndDispatch(t *testing.T) {
	for _, policy := range []bool{false, true} {
		for _, mode := range []string{"declared", "native", "late-native", "unknown-native", "unrelated-native", "different-policy-zone"} {
			if !policy && mode == "different-policy-zone" {
				continue
			}
			t.Run(fmt.Sprintf("policy=%v/%s", policy, mode), func(t *testing.T) {
				ctx := context.Background()
				writes := 0
				nameKey, idKey := "siteTagName", "siteTagId"
				if policy {
					nameKey, idKey = "policyTagName", "policyTagId"
				}
				row := map[string]any{"name": "T", "sites": []any{"Global/A"}}
				if policy {
					row["ap_zones"] = []any{"Z"}
				} else {
					row["ap_profile_name"] = "AP"
				}
				desired := []any{row}
				if mode == "declared" {
					other := map[string]any{}
					for k, v := range row {
						other[k] = v
					}
					other["name"] = "U"
					desired = append(desired, other)
				}
				tags, err := parseWirelessProfileTags(desired, policy)
				if err != nil {
					t.Fatal(err)
				}
				p := wirelessProfileIntent{name: "P"}
				if policy {
					p.policyTags = tags
				} else {
					p.siteTags = tags
				}
				foreign := mode != "declared" && mode != "late-native"
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					switch {
					case r.URL.Path == authPath:
						fmt.Fprint(w, `{"Token":"test"}`)
					case r.Method != "GET":
						writes++
						fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					case r.URL.Path == wirelessProfilesPath:
						fmt.Fprint(w, `{"response":[{"id":"profile","wirelessProfileName":"P","apZones":[{"apZoneName":"Z","ssids":[]},{"apZoneName":"Other","ssids":[]}]}]}`)
					case r.URL.Path == wirelessAPPath:
						fmt.Fprint(w, `{"response":[{"id":"ap","apProfileName":"AP"}]}`)
					case r.URL.Path == sitesPath:
						json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}, {ID: "a", Name: "A", NameHierarchy: "Global/A", ParentID: "global", Type: "area"}, {ID: "b", Name: "B", NameHierarchy: "Global/B", ParentID: "global", Type: "area"}}})
					case r.URL.Path == profileSiteAssignmentsPath("profile"):
						fmt.Fprint(w, `{"response":[{"id":"a"},{"id":"b"}]}`)
					case r.URL.Path == wirelessProfileTagPath("profile", policy):
						rows := []map[string]any{}
						if foreign {
							other := map[string]any{idKey: "foreign", nameKey: "Foreign", "siteIds": []string{"a"}}
							if policy {
								other["apZones"] = []string{"Z"}
							} else {
								other["apProfileName"] = "AP"
								other["flexProfileName"] = "default-flex-profile"
							}
							if mode == "unrelated-native" {
								other["siteIds"] = []string{"b"}
							}
							if mode == "different-policy-zone" {
								other["apZones"] = []string{"Other"}
							}
							if mode == "unknown-native" {
								other["unknownField"] = "unqualified"
							}
							rows = append(rows, other)
						}
						json.NewEncoder(w).Encode(map[string]any{"response": rows})
					default:
						t.Error("unexpected native read", r.URL.Path)
						w.WriteHeader(404)
					}
				})
				e := &wirelessProfileTagExecutor{client: c, desired: []wirelessProfileIntent{p}, policy: policy}
				write, err := e.next(ctx)
				switch mode {
				case "unrelated-native":
					if err != nil || write == nil {
						t.Fatal("nonconflicting scope refused", err)
					}
				case "different-policy-zone":
					if !errors.Is(err, errWirelessPolicyCohortRequired) || write != nil {
						t.Fatal("unqualified merge released plan", err)
					}
				case "late-native":
					if err != nil || write == nil {
						t.Fatal("initial plan refused", err)
					}
					foreign = true
					if _, err = write.submit(ctx); !errors.Is(err, errWirelessTagScopeConflict) {
						t.Fatal("late conflict did not fence submission", err)
					}
				case "unknown-native":
					if err == nil || write != nil {
						t.Fatal("unmodeled native scope accepted")
					}
				default:
					if !errors.Is(err, errWirelessTagScopeConflict) || write != nil {
						t.Fatal("overlap released plan", err)
					}
				}
				if writes != 0 {
					t.Fatal("scope check submitted a native mutation")
				}
			})
		}
	}
}
