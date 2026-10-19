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

func TestFabricL2L3Dependency(t *testing.T) {
	for _, mode := range []string{"attached", "missing", "unattached", "replaced", "detached", "wrong-readback", "vlan-collision", "gateway-name", "gateway-id", "gateway-other-fabric", "gateway-unavailable", "late-fabric", "late-vn", "late-existing"} {
		t.Run(mode, func(t *testing.T) {
			rows := []fabricL2Network{}
			writes := 0
			gateways := []any{}
			gatewayUnavailable := false
			late := false
			b := false
			if mode == "vlan-collision" {
				rows = append(rows, fabricL2Network{ID: "other", FabricID: "fabric", VLANName: "Other", VLAN: 150, Traffic: "DATA", Wireless: &b})
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case sitesPath:
					reply(sampleSites())
				case fabricSitesPath:
					fabricID := "fabric"
					if late && mode == "late-fabric" {
						fabricID = "replacement"
					}
					reply([]fabricSite{{ID: fabricID, SiteID: "area", Authentication: "Closed Authentication", PubSub: &b}})
				case fabricL3NetworksPath:
					networks := []fabricL3Network{}
					if mode != "missing" {
						n := fabricL3Network{ID: "l3", Name: "Corp", Fabrics: []string{"fabric"}}
						if mode == "unattached" || mode == "detached" && writes > 0 {
							n.Fabrics = nil
						}
						if mode == "replaced" && writes > 0 || mode == "late-vn" && late {
							n.ID = "replacement"
						}
						networks = append(networks, n)
					}
					reply(networks)
				case anycastGatewaysPath:
					if gatewayUnavailable {
						w.WriteHeader(503)
						return
					}
					reply(gateways)
				case fabricL2NetworksPath:
					if r.Method == "GET" {
						reply(rows)
						return
					}
					if r.Method != "POST" {
						t.Error("unexpected mutation")
						w.WriteHeader(400)
						return
					}
					writes++
					if json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 {
						t.Error("invalid body")
						w.WriteHeader(400)
						return
					}
					if rows[0].L3 != "Corp" || rows[0].VLAN != 150 {
						t.Error("lost association/VLAN")
					}
					rows[0].ID = "new"
					rows[0].Wireless = &b
					if mode == "wrong-readback" {
						rows[0].L3 = "Other"
					}
					reply(map[string]string{"taskId": "task"})
				default:
					t.Error("unexpected API")
					w.WriteHeader(404)
				}
			})
			e := &fabricL2Executor{client: c, desired: []fabricL2Intent{{path: "Global/Lab", name: "clients", vlanName: "Clients", traffic: "DATA", vlan: 150, l3: "Corp"}}}
			write, err := e.next(context.Background())
			if mode == "missing" || mode == "unattached" || mode == "vlan-collision" {
				if err == nil || write != nil || writes != 0 {
					t.Fatal("dependency conflict not fenced")
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal("failed to plan", err)
			}
			late = true
			switch mode {
			case "gateway-name", "gateway-id", "gateway-other-fabric":
				gateway := anycastObservationFixture()
				gateway["vlanId"] = 151
				gateway["vlanName"] = "Other"
				if mode == "gateway-id" {
					gateway["vlanId"] = 150
				} else {
					gateway["vlanName"] = "Clients"
				}
				if mode == "gateway-other-fabric" {
					gateway["fabricId"] = "other"
				}
				gateways = append(gateways, gateway)
			case "gateway-unavailable":
				gatewayUnavailable = true
			case "late-existing":
				rows = append(rows, fabricL2Network{ID: "other", FabricID: "fabric", VLANName: "Clients", VLAN: 150, Traffic: "DATA", Wireless: &b, L3: "Corp"})
			}
			_, submitErr := write.submit(context.Background())
			blocked := mode == "gateway-name" || mode == "gateway-id" || mode == "gateway-unavailable" || mode == "late-fabric" || mode == "late-vn" || mode == "late-existing"
			if blocked {
				if submitErr == nil || writes != 0 {
					t.Fatal("late conflict/dependency did not prevent write", submitErr, writes)
				}
				if (mode == "gateway-name" || mode == "gateway-id") && submitErr != errConfigurationTransition {
					t.Fatal("wrong conflict error", submitErr)
				}
				return
			}
			if submitErr != nil {
				t.Fatal(submitErr)
			}
			op := &cvk.NetworkControllerConfigurationOperation{Action: "Create", Resource: write.resource, ParentID: write.identity.parent}
			verified, err := e.verify(context.Background(), op)
			if verified != (mode == "attached" || mode == "gateway-other-fabric") {
				t.Fatal("bad dependency verification", err)
			}
			if mode == "attached" || mode == "gateway-other-fabric" {
				next, err := e.next(context.Background())
				if err != nil || next != nil || writes != 1 {
					t.Fatal("failed unchanged convergence", err)
				}
			}
		})
	}
}
