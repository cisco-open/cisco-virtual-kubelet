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

func TestProfileSiteTagProjectionScope(t *testing.T) {
	for _, mode := range []string{"complete", "other-candidate-device", "other-candidate-pnp", "other-candidate-error", "foreign-tag", "changed-projected-fields", "replaced-tag", "ancestor-reference", "unknown-projection"} {
		t.Run(mode, func(t *testing.T) {
			actual := wirelessProfileTagMetadata{id: "tag", name: "ST", profileID: "profile", fields: map[string]any{"siteTagName": "ST", "siteIds": []string{"b"}, "apProfileName": "AP", "flexProfileName": "default-flex-profile"}}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}, {ID: "a", Name: "A", NameHierarchy: "Global/A", ParentID: "global", Type: "area"}, {ID: "b", Name: "B", NameHierarchy: "Global/B", ParentID: "global", Type: "area"}, {ID: "c", Name: "C", NameHierarchy: "Global/C", ParentID: "global", Type: "area"}}})
				case wirelessProfilesPath:
					fmt.Fprint(w, `{"response":[{"id":"profile","wirelessProfileName":"P","ssidDetails":[]}]}`)
				case profileSiteAssignmentsPath("profile"):
					fmt.Fprint(w, `{"response":[{"id":"a"},{"id":"b"},{"id":"c"}]}`)
				case assignedDevicesPath:
					rows := []map[string]string{}
					if r.URL.Query().Get("siteId") == "c" {
						if mode == "other-candidate-error" {
							w.WriteHeader(503)
							return
						}
						if mode == "other-candidate-device" {
							rows = append(rows, map[string]string{"siteId": "c", "deviceId": "consumer"})
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case pnpDevicesPath:
					rows := []map[string]any{}
					if mode == "other-candidate-pnp" {
						rows = append(rows, map[string]any{"id": "pnp", "deviceInfo": map[string]any{"serialNumber": "FOC12345678", "pid": "C9300", "siteId": "c", "state": "Claimed", "onbState": "Planned"}})
					}
					json.NewEncoder(w).Encode(rows)
				case wirelessProfileTagPath("profile", false):
					row := map[string]any{"siteTagId": "tag", "siteTagName": "ST", "siteIds": []string{"b"}, "apProfileName": "AP", "flexProfileName": "default-flex-profile"}
					if mode == "changed-projected-fields" {
						row["apProfileName"] = "OTHER"
					}
					if mode == "replaced-tag" {
						row["siteTagId"] = "replacement"
					}
					rows := []map[string]any{row}
					if mode == "foreign-tag" {
						rows = append(rows, map[string]any{"siteTagId": "foreign", "siteTagName": "FOREIGN", "siteIds": []string{"c"}})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case wirelessProfileTagPath("profile", true):
					fmt.Fprint(w, `{"response":[]}`)
				default:
					t.Errorf("unexpected projected reader request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			})
			e := &profileSitePruneExecutor{client: c, rows: map[string]cvk.NetworkControllerConfigurationActionApplication{}, wanted: map[string]bool{}}
			for _, id := range []string{"b", "c"} {
				key := profileSiteDigest(tagMembershipIdentity("Global/"+strings.ToUpper(id), "P"))
				e.rows["wireless-sites/"+key] = cvk.NetworkControllerConfigurationActionApplication{Operation: cvk.NetworkControllerConfigurationOperation{Resource: "wireless-sites/" + profileSiteAddPrefix + key + ":" + strings.Repeat("a", 64), ParentID: "profile", TargetID: id, Action: "Update"}}
			}
			payload := map[string]any{"siteTagName": "ST", "siteIds": []string{"a"}, "apProfileName": "AP", "flexProfileName": "default-flex-profile"}
			if mode == "ancestor-reference" {
				payload["siteIds"] = []string{"global"}
			}
			if mode == "unknown-projection" {
				payload["siteIds"] = nil
			}
			projected := profileSiteTagProjection{profileSiteTagIdentity("profile", "tag", false): {profileID: "profile", tagID: "tag", actual: &actual, payload: payload}}
			rows, err := e.snapshotWithTags(context.Background(), projected)
			if mode == "complete" {
				if err != nil || len(rows) != 2 {
					t.Fatal("complete two-candidate projection failed", err)
				}
				if _, err := e.snapshot(context.Background()); err == nil {
					t.Fatal("prospective projection leaked into strict deletion observation")
				}
			} else if err == nil {
				t.Fatal("unqualified candidate or projection was ignored")
			}
		})
	}
}
