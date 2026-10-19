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

func TestWirelessProfileTagInventory(t *testing.T) {
	for _, policy := range []bool{false, true} {
		t.Run(fmt.Sprint(policy), func(t *testing.T) {
			idKey, nameKey := "siteTagId", "siteTagName"
			if policy {
				idKey, nameKey = "policyTagId", "policyTagName"
			}
			row := map[string]any{idKey: "tag-id", nameKey: "T", "siteIds": []string{"site-z", "site-a"}, "unknownSecret": "secret-sentinel"}
			if policy {
				row["apZones"] = []string{"Z", "A"}
			} else {
				row["apProfileName"] = "AP"
				row["flexProfileName"] = "default-flex-profile"
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != wirelessProfileTagPath("profile-id", policy) || r.Method != "GET" || r.URL.Query().Get("limit") != "500" || r.URL.Query().Get("offset") != "1" {
					t.Error("incorrect tag request")
				}
				json.NewEncoder(w).Encode(map[string]any{"response": []any{row}})
			})
			rows, err := c.wirelessProfileTagInventory(context.Background(), "profile-id", policy)
			if err != nil {
				t.Fatal(err)
			}
			got := rows["T"]
			if got.id != "tag-id" || got.profileID != "profile-id" || !got.unmapped || got.fields["siteIds"].([]string)[0] != "site-a" {
				t.Fatal("identity or normalization lost")
			}
			raw, _ := json.Marshal(got.fields)
			if strings.Contains(string(raw), "secret-sentinel") || strings.Contains(string(raw), "unknownSecret") {
				t.Fatal("unknown field retained")
			}
			if _, err = c.wirelessProfileTagInventory(context.Background(), "../profile", policy); err == nil {
				t.Fatal("unsafe parent accepted")
			}
		})
	}
}
func TestWirelessProfileTagInventoryMalformed(t *testing.T) {
	for name, rows := range map[string]string{
		"duplicate name":     `[{"siteTagId":"one","siteTagName":"T","siteIds":[]},{"siteTagId":"two","siteTagName":"T","siteIds":[]}]`,
		"duplicate identity": `[{"siteTagId":"one","siteTagName":"T","siteIds":[]},{"siteTagId":"one","siteTagName":"U","siteIds":[]}]`,
		"missing sites":      `[{"siteTagId":"one","siteTagName":"T"}]`,
		"invalid site":       `[{"siteTagId":"one","siteTagName":"T","siteIds":["../bad"]}]`,
		"duplicate site":     `[{"siteTagId":"one","siteTagName":"T","siteIds":["site","site"]}]`,
		"invalid type":       `[{"siteTagId":"one","siteTagName":"T","siteIds":[],"flexProfileName":true}]`,
	} {
		t.Run(name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				fmt.Fprintf(w, `{"response":%s}`, rows)
			})
			if _, err := c.wirelessProfileTagInventory(context.Background(), "profile", false); err == nil {
				t.Fatal("malformed tag accepted")
			}
		})
	}
}

func TestWirelessProfileTagNullLoadSentinel(t *testing.T) {
	for _, policy := range []bool{false, true} {
		for _, nonNull := range []bool{false, true} {
			t.Run(fmt.Sprintf("policy=%v/nonNull=%v", policy, nonNull), func(t *testing.T) {
				idKey, nameKey := "siteTagId", "siteTagName"
				if policy {
					idKey, nameKey = "policyTagId", "policyTagName"
				}
				row := map[string]any{idKey: "tag", nameKey: "T", "siteIds": []string{"site"}, "load": nil}
				if nonNull {
					row["load"] = "unqualified-sentinel"
				}
				if policy {
					row["apZones"] = []string{}
				}
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == authPath {
						fmt.Fprint(w, `{"Token":"test"}`)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"response": []any{row}})
				})
				got, err := c.wirelessProfileTagInventory(context.Background(), "profile", policy)
				if err != nil || got["T"].unmapped != nonNull {
					t.Fatal("wrong sentinel qualification", err)
				}
				if _, retained := got["T"].fields["load"]; retained {
					t.Fatal("observation sentinel became configuration")
				}
			})
		}
	}
}
