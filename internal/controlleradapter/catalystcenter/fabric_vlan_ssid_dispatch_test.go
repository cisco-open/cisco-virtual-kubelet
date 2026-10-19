// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"testing"
)

func TestFabricVLANSSIDOwnershipSnapshot(t *testing.T) {
	for _, mode := range []string{"ready", "missing-owner", "foreign-owner", "duplicate-owner", "changed-status"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			owner := cvk.NetworkControllerSectionOwner{Section: "fabric", ConfigName: config.Name, ConfigUID: string(config.UID)}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{owner}}
			switch mode {
			case "missing-owner":
				nc.Status.Configuration.Owners = nil
			case "foreign-owner":
				nc.Status.Configuration.Owners[0].ConfigUID = "replacement"
			case "duplicate-owner":
				nc.Status.Configuration.Owners = append(nc.Status.Configuration.Owners, owner)
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			reads := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("ownership snapshot mutated controller")
					w.WriteHeader(400)
					return
				}
				reads++
				if mode == "changed-status" && reads == 1 {
					current, err := a.currentController(ctx)
					if err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					current.Status.Configuration.Owners = append(current.Status.Configuration.Owners, cvk.NetworkControllerSectionOwner{Section: "inventory", ConfigName: config.Name, ConfigUID: string(config.UID)})
					if err := a.statusWriter.Status().Update(ctx, current); err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
				}
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				switch r.URL.Path {
				case sitesPath:
					reply(sampleSites())
				case fabricSitesPath:
					b := false
					reply([]fabricSite{{ID: "fabric", SiteID: "area", Authentication: "No Authentication", PubSub: &b}})
				case "/dna/intent/api/v1/sda/fabrics/fabric/vlanToSsids":
					reply([]fabricVLANSSID{})
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			owned, err := a.currentFabricVLANSSIDOwnership(ctx, config, "Global/Lab")
			if mode == "ready" {
				if err != nil || owned == nil || len(owned) != 0 {
					t.Fatal("empty ownership", owned, err)
				}
			} else {
				if err == nil || owned != nil {
					t.Fatal("invalid ownership snapshot accepted")
				}
				if mode != "changed-status" && reads != 0 {
					t.Fatal("native reads without section owner")
				}
			}
			prior := reads
			if _, err := a.dispatchFabricVLANSSIDs(ctx, config, nil, "Global/Lab", nil); err == nil || reads != prior {
				t.Fatal("nil permit admitted")
			}
		})
	}
}
