// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"strconv"
	"testing"
)

func TestFabricZoneParentBoundCreation(t *testing.T) {
	for _, mode := range []string{"create", "missing-parent", "replaced-parent", "replaced-hierarchy", "wrong-auth", "bad-receipt"} {
		t.Run(mode, func(t *testing.T) {
			zones := []fabricZone{}
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case sitesPath:
					sites := sampleSites()
					if mode == "replaced-hierarchy" && writes > 0 {
						sites[2].ID = "replacement"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": sites})
				case fabricSitesPath:
					rows := []fabricSite{}
					pubsub := false
					id := "fabric"
					if mode == "replaced-parent" && writes > 0 {
						id = "replacement"
					}
					if mode != "missing-parent" {
						rows = append(rows, fabricSite{ID: id, SiteID: "area", Authentication: "Closed Authentication", PubSub: &pubsub})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case fabricZonesPath:
					if r.Method == "POST" {
						writes++
						var rows []fabricZone
						if json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 || rows[0].SiteID != "building" || rows[0].ID != "" {
							t.Error("wrong zone payload")
							w.WriteHeader(400)
							return
						}
						rows[0].ID = "zone"
						if mode == "wrong-auth" {
							rows[0].Authentication = "Open Authentication"
						}
						zones = rows
						if mode == "bad-receipt" {
							fmt.Fprint(w, `{"response":{}}`)
						} else {
							fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
						}
					} else {
						json.NewEncoder(w).Encode(map[string]any{"response": zones})
					}
				default:
					t.Error("unexpected API")
					w.WriteHeader(404)
				}
			})
			e := &fabricZoneExecutor{client: c, desired: []fabricZoneIntent{{path: "Global/Lab/Building", parent: "Global/Lab", authentication: "Closed Authentication"}}}
			mutation, err := e.next(context.Background())
			if mode == "missing-parent" {
				if err == nil || mutation != nil || writes != 0 {
					t.Fatal("missing parent treated as converged")
				}
				return
			}
			if err != nil || mutation == nil {
				t.Fatal(err)
			}
			task, err := mutation.submit(context.Background())
			if mode == "bad-receipt" {
				if err == nil || writes != 1 {
					t.Fatal("missing receipt accepted/replayed")
				}
				return
			}
			if err != nil || task.ID != "task" {
				t.Fatal(err)
			}
			op := &cvk.NetworkControllerConfigurationOperation{Action: "Create", Resource: mutation.resource, ParentID: mutation.identity.parent}
			verified, err := e.verify(context.Background(), op)
			if verified != (mode == "create") {
				t.Fatal("incorrect verification", err)
			}
			if mode == "create" {
				if err != nil {
					t.Fatal(err)
				}
				next, err := e.next(context.Background())
				if err != nil || next != nil {
					t.Fatal("creation replayed", err)
				}
			}
			if writes != 1 {
				t.Fatal("readback replayed mutation")
			}
		})
	}
}
func TestFabricZonePaging(t *testing.T) {
	for _, mode := range []string{"complete", "repeat", "partial"} {
		t.Run(mode, func(t *testing.T) {
			pages := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				pages++
				offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
				if r.URL.Query().Get("limit") != "50" {
					t.Error("wrong fabric page bound")
				}
				if mode == "repeat" {
					offset = 1
				}
				rows := []fabricZone{}
				for i := offset; i < offset+50 && i <= 51; i++ {
					rows = append(rows, fabricZone{ID: fmt.Sprint("zone-", i), SiteID: fmt.Sprint("site-", i), Authentication: "No Authentication"})
				}
				if mode == "partial" {
					w.WriteHeader(206)
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			got, err := c.fabricZones(context.Background())
			if mode == "complete" {
				if err != nil || len(got) != 51 || pages != 2 {
					t.Fatal("truncated inventory", err)
				}
			} else if err == nil || got != nil {
				t.Fatal("partial/repeated inventory accepted")
			}
		})
	}
}
