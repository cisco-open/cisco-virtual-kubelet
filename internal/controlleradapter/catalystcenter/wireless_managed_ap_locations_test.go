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

func TestWirelessManagedAPLocationPaging(t *testing.T) {
	for _, kind := range []string{"primary", "secondary", "anchor"} {
		t.Run(kind, func(t *testing.T) {
			pages, counts := 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/managedApLocations/count") {
					counts++
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]int{"primaryManagedApLocationsCount": 501, "secondaryManagedApLocationsCount": 501, "anchorManagedApLocationsCount": 501}})
					return
				}
				if r.URL.Path != "/dna/intent/api/v1/wirelessControllers/wlc/"+kind+"ManagedApLocations" || r.Method != "GET" || r.URL.Query().Get("limit") != "500" {
					t.Error("wrong inventory request")
				}
				pages++
				start, end := 1, 501
				if r.URL.Query().Get("offset") == "501" {
					start, end = 501, 502
				} else if r.URL.Query().Get("offset") != "1" {
					t.Error("wrong offset")
				}
				rows := []wirelessManagedAPLocation{}
				for i := start; i < end; i++ {
					rows = append(rows, wirelessManagedAPLocation{SiteID: fmt.Sprint("site-", i), SitePath: fmt.Sprint("Global/Lab", i)})
				}
				json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"managedApLocations": rows}}})
			})
			got, err := c.wirelessManagedAPLocations(context.Background(), "wlc", kind)
			if err != nil || len(got) != 501 || pages != 2 || counts != 2 {
				t.Fatal(len(got), pages, counts, err)
			}
		})
	}
}
func TestWirelessManagedAPLocationInvalidInventory(t *testing.T) {
	for _, scenario := range []string{"missing-array", "duplicate-id", "duplicate-path", "bad-site", "truncated", "count-changed"} {
		t.Run(scenario, func(t *testing.T) {
			counts := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/count") {
					counts++
					n := 1
					if scenario == "truncated" || scenario == "duplicate-id" || scenario == "duplicate-path" || scenario == "count-changed" && counts == 2 {
						n = 2
					}
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]int{"primaryManagedApLocationsCount": n, "secondaryManagedApLocationsCount": 0, "anchorManagedApLocationsCount": 0}})
					return
				}
				rows := []wirelessManagedAPLocation{{SiteID: "site", SitePath: "Global/Lab"}}
				switch scenario {
				case "missing-array":
					fmt.Fprint(w, `{"response":[{}]}`)
					return
				case "duplicate-id":
					rows = append(rows, rows[0])
				case "duplicate-path":
					rows = append(rows, wirelessManagedAPLocation{SiteID: "site2", SitePath: "Global/Lab"})
				case "bad-site":
					rows[0].SiteID = "../bad"
				}
				json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"managedApLocations": rows}}})
			})
			if _, err := c.wirelessManagedAPLocations(context.Background(), "wlc", "primary"); err == nil {
				t.Fatal("invalid inventory accepted")
			}
		})
	}
	if _, err := (&client{}).wirelessManagedAPLocations(context.Background(), "wlc", "unknown"); err == nil {
		t.Fatal("invalid kind")
	}
}
