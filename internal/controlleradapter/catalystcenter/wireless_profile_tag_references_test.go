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

func TestWirelessProfileTagReferenceCollection(t *testing.T) {
	for _, policy := range []bool{false, true} {
		t.Run(fmt.Sprint(policy), func(t *testing.T) {
			profile := wirelessProfileMetadata{id: "profile", name: "P", fields: map[string]any{"apZones": []map[string]any{{"apZoneName": "Zone"}}}}
			row := map[string]any{"name": "Tag", "sites": []any{"Global/Lab"}}
			if policy {
				row["ap_zones"] = []any{"Zone"}
			} else {
				row["ap_profile_name"] = "AP"
			}
			tags, err := parseWirelessProfileTags([]any{row}, policy)
			if err != nil {
				t.Fatal(err)
			}
			assigned := true
			apReads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}, {ID: "site", Name: "Lab", NameHierarchy: "Global/Lab", Type: "area", ParentID: "global"}}})
				case profileSiteAssignmentsPath("profile"):
					rows := []map[string]string{}
					if assigned {
						rows = append(rows, map[string]string{"id": "site"})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case wirelessAPPath:
					apReads++
					fmt.Fprint(w, `{"response":[{"id":"ap-id","apProfileName":"AP"}]}`)
				default:
					t.Errorf("unexpected dependency %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			refs, err := c.wirelessProfileTagReferences(context.Background(), profile, tags[0], nil, policy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tags[0].payload(refs, nil, policy); err != nil {
				t.Fatal("binding failed", err)
			}
			if policy && apReads != 0 || !policy && apReads != 1 {
				t.Fatal("unnecessary or missing AP lookup")
			}
			assigned = false
			refs, err = c.wirelessProfileTagReferences(context.Background(), profile, tags[0], nil, policy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tags[0].payload(refs, nil, policy); err == nil {
				t.Fatal("unassigned site accepted")
			}
			if !policy {
				tags[0].fields["flexProfileName"] = "custom-flex"
				if _, err = c.wirelessProfileTagReferences(context.Background(), profile, tags[0], nil, false); err != errWirelessCustomFlexInventory {
					t.Fatal("unqualified custom reference accepted", err)
				}
			}
		})
	}
}
