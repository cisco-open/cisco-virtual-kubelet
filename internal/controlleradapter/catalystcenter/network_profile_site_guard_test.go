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

func TestProfileSitePlansRevalidateCompleteSelection(t *testing.T) {
	for _, wireless := range []bool{false, true} {
		for _, mode := range []string{"stable", "first-profile", "later-profile", "first-site", "later-site", "missing-later-site", "mid-read-site", "became-present", "later-present", "caller-mutated", "unrelated", "lost-ack", "read-error"} {
			t.Run(fmt.Sprintf("wireless=%t/%s", wireless, mode), func(t *testing.T) {
				profiles, sites := []string{"p-id", "q-id"}, []string{"a-id", "b-id", "c-id"}
				assigned := map[string]map[string]bool{"p-id": {"foreign-site": true}, "q-id": {}}
				changed, writes, siteReads, requests := false, 0, 0, 0
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == authPath {
						fmt.Fprint(w, `{"Token":"test"}`)
						return
					}
					requests++
					if r.Method == http.MethodPost {
						var body map[string]string
						if r.URL.Path != profileSiteAssignmentsPath("p-id") || json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["id"] != "a-id" {
							t.Error("assignment destination or body changed")
						}
						writes++
						assigned["p-id"]["a-id"] = true
						if mode == "lost-ack" {
							fmt.Fprint(w, `{}`)
						} else {
							fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
						}
						return
					}
					switch r.URL.Path {
					case networkProfilesPath, wirelessProfilesPath:
						if wireless != (r.URL.Path == wirelessProfilesPath) {
							t.Error("profile namespace changed")
						}
						rows := []any{}
						for i, name := range []string{"P", "Q"} {
							if wireless {
								rows = append(rows, map[string]string{"id": profiles[i], "wirelessProfileName": name})
							} else {
								rows = append(rows, networkProfile{ID: profiles[i], Name: name, Namespace: "switching"})
							}
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
					case sitesPath:
						siteReads++
						if changed && mode == "read-error" {
							w.WriteHeader(400)
							return
						}
						rows := []Site{{ID: "root", Name: "Global", NameHierarchy: "Global", Type: "global"}}
						for i, name := range []string{"A", "B", "C"} {
							if changed && mode == "missing-later-site" && i == 1 {
								continue
							}
							rows = append(rows, Site{ID: sites[i], Name: name, NameHierarchy: "Global/" + name, Type: "area", ParentID: "root"})
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
					default:
						id := ""
						for _, candidate := range profiles {
							if r.URL.Path == profileSiteAssignmentsPath(candidate) {
								id = candidate
							}
						}
						if id == "" {
							t.Error("unexpected assignment endpoint")
							http.NotFound(w, r)
							return
						}
						rows := []map[string]string{}
						for member := range assigned[id] {
							rows = append(rows, map[string]string{"id": member})
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
						if changed && mode == "mid-read-site" && siteReads == 2 {
							sites[0] = "replacement-a"
						}
					}
				})
				e := &profileSiteExecutor{client: c, wireless: wireless, desired: []profileSiteIntent{{name: "P", sites: []string{"Global/A", "Global/B"}}, {name: "Q", sites: []string{"Global/C"}}}}
				write, err := e.next(context.Background())
				if err != nil || write == nil {
					t.Fatal("initial assignment plan failed", err)
				}
				changed = true
				switch mode {
				case "first-profile":
					profiles[0] = "replacement-p"
				case "later-profile":
					profiles[1] = "replacement-q"
				case "first-site":
					sites[0] = "replacement-a"
				case "later-site":
					sites[1] = "replacement-b"
				case "became-present":
					assigned["p-id"]["a-id"] = true
				case "later-present":
					assigned["p-id"]["b-id"] = true
				case "unrelated":
					assigned["p-id"]["another-foreign-site"] = true
				case "caller-mutated":
					e.wireless = !wireless
					e.desired[0].name = "changed"
					e.desired[0].sites[0] = "Global/Changed"
				}
				_, err = write.submit(context.Background())
				allowed := mode == "stable" || mode == "unrelated" || mode == "caller-mutated"
				if allowed && (err != nil || writes != 1) {
					t.Fatal("unchanged selection failed", err, writes)
				}
				if !allowed && (err == nil || mode != "lost-ack" && writes != 0 || mode == "lost-ack" && writes != 1) {
					t.Fatal("stale or uncertain selection was submitted", writes)
				}
				beforeReads, beforeWrites := requests, writes
				if _, err := write.submit(context.Background()); err == nil || requests != beforeReads || writes != beforeWrites {
					t.Fatal("consumed assignment plan was reused")
				}
				if !assigned["p-id"]["foreign-site"] {
					t.Fatal("foreign membership lost")
				}
			})
		}
	}
}

func TestProfileSiteRecoveryReadsOnlyAcknowledgedPair(t *testing.T) {
	for _, wireless := range []bool{false, true} {
		for _, replace := range []bool{false, true} {
			t.Run(fmt.Sprintf("wireless=%t/late-replace=%t", wireless, replace), func(t *testing.T) {
				site, reads := "site", 0
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == authPath {
						fmt.Fprint(w, `{"Token":"test"}`)
						return
					}
					if r.Method != http.MethodGet {
						t.Error("recovery mutated native state")
					}
					switch r.URL.Path {
					case networkProfilesPath:
						_ = json.NewEncoder(w).Encode(map[string]any{"response": []networkProfile{{ID: "profile", Name: "P", Namespace: "switching"}}})
					case wirelessProfilesPath:
						fmt.Fprint(w, `{"response":[{"id":"profile","wirelessProfileName":"P"}]}`)
					case sitesPath:
						_ = json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "root", Name: "Global", NameHierarchy: "Global", Type: "global"}, {ID: site, Name: "A", NameHierarchy: "Global/A", Type: "area", ParentID: "root"}}})
					case profileSiteAssignmentsPath("profile"):
						reads++
						fmt.Fprint(w, `{"response":[{"id":"site"}]}`)
						if replace && reads == 1 {
							site = "replacement"
						}
					default:
						t.Error("recovery read unrelated assignment")
						http.NotFound(w, r)
					}
				})
				e := &profileSiteExecutor{client: c, wireless: wireless, desired: []profileSiteIntent{{name: "P", sites: []string{"Global/A", "Global/Missing"}}, {name: "Missing", sites: []string{"Global/Missing"}}}}
				op := &cvk.NetworkControllerConfigurationOperation{Action: "Update", Resource: tagMembershipIdentity("Global/A", "P"), TargetID: "site", ParentID: "profile"}
				encoded, _ := json.Marshal(op)
				var recovered cvk.NetworkControllerConfigurationOperation
				if err := json.Unmarshal(encoded, &recovered); err != nil {
					t.Fatal(err)
				}
				ok, err := e.verify(context.Background(), &recovered)
				if !replace && (err != nil || !ok || reads != 2) {
					t.Fatal("acknowledged pair did not verify independently", err, reads)
				}
				if replace && (err == nil || ok) {
					t.Fatal("late replacement verified")
				}
			})
		}
	}
}

func TestProfileSiteDuplicateSelectionRefused(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		switch r.URL.Path {
		case networkProfilesPath:
			fmt.Fprint(w, `{"response":[{"siteProfileUuid":"profile","name":"P","namespace":"switching"}]}`)
		case sitesPath:
			fmt.Fprint(w, `{"response":[{"id":"root","name":"Global","nameHierarchy":"Global","type":"global"},{"id":"site","name":"A","nameHierarchy":"Global/A","type":"area","parentId":"root"}]}`)
		default:
			if !strings.HasSuffix(r.URL.Path, "/siteAssignments") {
				t.Error("unexpected endpoint")
			}
			fmt.Fprint(w, `{"response":[]}`)
		}
	})
	e := &profileSiteExecutor{client: c, desired: []profileSiteIntent{{name: "P", sites: []string{"Global/A", "Global/A"}}}}
	if write, err := e.next(context.Background()); err == nil || write != nil {
		t.Fatal("ambiguous duplicate assignment planned")
	}
}
