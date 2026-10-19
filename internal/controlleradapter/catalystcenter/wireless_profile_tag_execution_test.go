// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestWirelessProfileTagNativeCycle(t *testing.T) {
	for _, policy := range []bool{false, true} {
		t.Run(fmt.Sprint(policy), func(t *testing.T) {
			ctx := context.Background()
			profileID, siteA := "profile-id", "site-a"
			nameKey, idKey := "siteTagName", "siteTagId"
			if policy {
				nameKey, idKey = "policyTagName", "policyTagId"
			}
			row := map[string]any{"name": "T", "sites": []any{"Global/A"}}
			if !policy {
				row["ap_profile_name"] = "AP"
			}
			if policy {
				row["ap_zones"] = []any{"Z"}
			}
			tags, err := parseWirelessProfileTags([]any{row}, policy)
			if err != nil {
				t.Fatal(err)
			}
			p := wirelessProfileIntent{name: "P"}
			if policy {
				p.policyTags = tags
			} else {
				p.siteTags = tags
			}
			observed := []map[string]any{}
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == wirelessProfilesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"id": profileID, "wirelessProfileName": "P", "apZones": []any{map[string]any{"apZoneName": "Z", "ssids": []string{}}}}}})
				case r.URL.Path == wirelessAPPath:
					fmt.Fprint(w, `{"response":[{"id":"ap-id","apProfileName":"AP"}]}`)
				case r.URL.Path == sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}, {ID: siteA, Name: "A", NameHierarchy: "Global/A", Type: "area", ParentID: "global"}, {ID: "site-b", Name: "B", NameHierarchy: "Global/B", Type: "area", ParentID: "global"}}})
				case r.URL.Path == profileSiteAssignmentsPath(profileID):
					json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]string{"id": siteA}, map[string]string{"id": "site-b"}}})
				case r.Method == "GET" && r.URL.Path == wirelessProfileTagPath(profileID, policy):
					json.NewEncoder(w).Encode(map[string]any{"response": observed})
				case r.Method == "POST" && r.URL.Path == wirelessProfileTagPath(profileID, policy)+"/bulk" || r.Method == "PUT" && r.URL.Path == wirelessProfileTagPath(profileID, policy)+"/tag-id":
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Error("bad JSON")
					}
					if r.Method == "POST" {
						items, ok := body["items"].([]any)
						if !ok || len(items) != 1 {
							t.Error("bad create envelope")
							w.WriteHeader(400)
							return
						}
						body = items[0].(map[string]any)
					}
					if body[nameKey] != "T" || body[idKey] != nil {
						t.Error("wrong tag payload")
					}
					body[idKey] = "tag-id"
					observed = []map[string]any{body}
					writes++
					fmt.Fprint(w, `{"response":{"taskId":"task-id"}}`)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			})
			e := &wirelessProfileTagExecutor{client: c, desired: []wirelessProfileIntent{p}, policy: policy}
			run := func(action string) *ciskov1.NetworkControllerConfigurationOperation {
				t.Helper()
				write, err := e.next(ctx)
				if err != nil || write == nil || write.action != action {
					t.Fatal("plan", err)
				}
				if _, err = write.submit(ctx); err != nil {
					t.Fatal(err)
				}
				op := &ciskov1.NetworkControllerConfigurationOperation{Action: action, Resource: write.resource, ParentID: write.identity.parent, TargetID: write.identity.target}
				restarted := *e
				if ok, err := restarted.verify(ctx, op); err != nil || !ok {
					t.Fatal("readback", err)
				}
				if write, err = e.next(ctx); err != nil || write != nil {
					t.Fatal("no-op", err)
				}
				return op
			}
			planned, err := e.next(ctx)
			if err != nil || planned == nil {
				t.Fatal("initial plan", err)
			}
			siteA = "replacement-before-submit"
			if _, err = planned.submit(ctx); err == nil || writes != 0 {
				t.Fatal("stale dependency submitted")
			}
			siteA = "site-a"
			run("Create")
			if policy {
				e.desired[0].policyTags[0].sites = append(tags[0].sites, "Global/B")
			} else {
				e.desired[0].siteTags[0].sites = append(tags[0].sites, "Global/B")
			}
			op := run("Update")
			if writes != 2 {
				t.Fatal("replayed")
			}
			siteA = "replacement-site"
			if ok, err := e.verify(ctx, op); err == nil || ok {
				t.Fatal("replacement site verified")
			}
			siteA = "site-a"
			if policy {
				e.desired[0].policyTags[0].sites = []string{"Global/A"}
			} else {
				e.desired[0].siteTags[0].sites = []string{"Global/A"}
			}
			if write, err := e.next(ctx); err == nil || write != nil {
				t.Fatal("unowned membership removal allowed")
			}
			profileID = "replacement-profile"
			if ok, err := e.verify(ctx, op); err == nil || ok {
				t.Fatal("replacement parent verified")
			}
		})
	}
}
