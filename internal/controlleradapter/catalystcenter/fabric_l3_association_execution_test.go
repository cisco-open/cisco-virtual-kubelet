// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"reflect"
	"testing"
)

func TestFabricL3AssociationParentBinding(t *testing.T) {
	for _, mode := range []string{"ordered", "zone-only-missing-parent", "replaced-zone-before-submit", "replaced-parent-after-submit", "replaced-hierarchy-after-submit", "lost-parent-membership", "missing-network", "missing-fabric"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			b := false
			writes := 0
			sites := []fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &b}}
			zones := []fabricZone{{ID: "zone", SiteID: "building", Authentication: "No Authentication"}}
			hierarchy := sampleSites()
			n := fabricL3Network{ID: "network", Name: "Corp", Fabrics: []string{"outside"}}
			if mode == "missing-fabric" {
				sites = []fabricSite{}
				zones = []fabricZone{}
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case sitesPath:
					reply(hierarchy)
				case fabricSitesPath:
					reply(sites)
				case fabricZonesPath:
					reply(zones)
				case fabricL3NetworksPath:
					if r.Method == "GET" {
						rows := []fabricL3Network{n}
						if mode == "missing-network" {
							rows = nil
						}
						reply(rows)
						return
					}
					if r.Method != "PUT" {
						t.Error("wrong method")
						w.WriteHeader(400)
						return
					}
					var rows []fabricL3Network
					if json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 {
						t.Error("invalid body")
						w.WriteHeader(400)
						return
					}
					writes++
					want := []string{"fabric", "outside"}
					if writes == 2 {
						want = append(want, "zone")
					}
					if !reflect.DeepEqual(rows[0].Fabrics, want) {
						t.Error("wrong membership order/dependency", rows[0].Fabrics)
					}
					n = rows[0]
					if writes == 2 {
						switch mode {
						case "replaced-parent-after-submit":
							sites[0].ID = "replacement"
						case "replaced-hierarchy-after-submit":
							hierarchy[2].ID = "replacement"
							zones[0].SiteID = "replacement"
						case "lost-parent-membership":
							n.Fabrics = []string{"outside", "zone"}
						}
					}
					reply(map[string]string{"taskId": fmt.Sprintf("task-%d", writes)})
				default:
					t.Error("unexpected API")
					w.WriteHeader(404)
				}
			})
			desired := []fabricL3AssociationIntent{{path: "Global/Lab", name: "Corp"}, {path: "Global/Lab/Building", parent: "Global/Lab", name: "Corp"}}
			if mode == "zone-only-missing-parent" {
				desired = desired[1:]
			}
			for step := 0; step < 2; step++ {
				e := &fabricL3AssociationExecutor{client: c, desired: desired}
				write, err := e.next(ctx)
				if mode == "zone-only-missing-parent" || mode == "missing-network" || mode == "missing-fabric" {
					if err == nil || write != nil || writes != 0 {
						t.Fatal("unavailable dependency did not fence")
					}
					return
				}
				if err != nil || write == nil {
					t.Fatal("failed to plan", err)
				}
				if step == 1 && mode == "replaced-zone-before-submit" {
					zones[0].ID = "replacement"
				}
				_, err = write.submit(ctx)
				if step == 1 && mode == "replaced-zone-before-submit" {
					if err == nil || writes != 1 {
						t.Fatal("submitted to replaced zone")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				op := &cvk.NetworkControllerConfigurationOperation{Action: write.action, Resource: write.resource, TargetID: write.identity.target, ParentID: write.identity.parent}
				restarted := &fabricL3AssociationExecutor{client: c, desired: desired}
				verified, err := restarted.verify(ctx, op)
				if step == 1 && mode != "ordered" {
					if verified {
						t.Fatal("replacement/lost parent verified")
					}
					return
				}
				if !verified || err != nil {
					t.Fatal("failed verification", err)
				}
			}
			e := &fabricL3AssociationExecutor{client: c, desired: desired}
			next, err := e.next(ctx)
			if err != nil || next != nil || writes != 2 {
				t.Fatal("no-op replayed", err)
			}
			drift, err := e.report(ctx)
			if err != nil || len(drift) != 0 {
				t.Fatal("bad converged report", err)
			}
		})
	}
}
