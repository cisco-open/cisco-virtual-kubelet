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

func TestAnycastGatewayNativeCreation(t *testing.T) {
	for _, mode := range []string{"create", "zone", "lost-receipt", "stale-before-submit", "changed-pool-after-submit", "wrong-readback", "incomplete-model", "name-conflict-before-submit", "id-conflict-before-submit", "zone-conflict-before-submit", "other-fabric-allocation", "l2-name-conflict", "l2-id-conflict", "l2-zone-conflict", "l2-other-fabric", "l2-unavailable"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			b := false
			writes := 0
			reservations := fixtureReservations()
			gateways := []any{}
			layer2 := []fabricL2Network{}
			l2Unavailable := false
			model := map[string]any{"ip_pool_name": "access", "l3_virtual_network": "Corp", "traffic_type": "DATA", "auto_generate_vlan_name": true, "critical_pool": false, "layer2_flooding": false, "wireless_pool": false, "ip_directed_broadcast": false, "intra_subnet_routing_enabled": false, "multiple_ip_to_mac_addresses": false}
			if mode == "incomplete-model" {
				delete(model, "wireless_pool")
			}
			if mode == "id-conflict-before-submit" || mode == "l2-id-conflict" {
				model["vlan_id"] = 150
			}
			customName := mode == "l2-name-conflict" || mode == "l2-other-fabric"
			expectedName := "192.0.2.128/25-Corp"
			if customName {
				model["auto_generate_vlan_name"] = false
				model["vlan_name"] = "NativeL2"
				expectedName = "NativeL2"
			}
			intent, err := parseAnycastGateway(model)
			if err != nil {
				t.Fatal(err)
			}
			want := fabricAnycastIntent{path: "Global/Lab", anycastGatewayIntent: intent}
			if mode == "zone" || mode == "zone-conflict-before-submit" || mode == "l2-zone-conflict" {
				want.path = "Global/Lab/Building"
				want.parent = "Global/Lab"
				p := map[string]any{}
				for k, v := range intent.payload {
					if k != "autoGenerateVlanName" {
						p[k] = v
					}
				}
				p["id"] = "parent"
				p["fabricId"] = "fabric"
				p["vlanId"] = 150
				p["vlanName"] = "192.0.2.128/25-Corp"
				gateways = append(gateways, p)
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case sitesPath:
					reply(sampleSites())
				case fabricSitesPath:
					reply([]fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &b}})
				case fabricZonesPath:
					reply([]fabricZone{{ID: "zone", SiteID: "building", Authentication: "No Authentication"}})
				case fabricL3NetworksPath:
					reply([]fabricL3Network{{ID: "network", Name: "Corp", Fabrics: []string{"fabric", "zone"}}})
				case globalPoolsPath:
					reply(fixturePools())
				case sitePoolsPath:
					reply(reservations)
				case fabricL2NetworksPath:
					if l2Unavailable {
						w.WriteHeader(503)
						return
					}
					reply(layer2)
				case anycastGatewaysPath:
					if r.Method == "GET" {
						reply(gateways)
						return
					}
					if r.Method != "POST" {
						t.Error("wrong verb")
						w.WriteHeader(400)
						return
					}
					writes++
					var body []map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 {
						t.Error("bad body")
						w.WriteHeader(400)
						return
					}
					n := body[0]
					target := "fabric"
					if mode == "zone" || mode == "zone-conflict-before-submit" || mode == "l2-zone-conflict" {
						target = "zone"
						if n["vlanId"] != float64(150) {
							t.Error("missing parent allocation")
						}
					}
					if n["fabricId"] != target || n["id"] != nil || n["autoGenerateVlanName"] != !customName || (!customName && n["vlanName"] != nil) || (customName && n["vlanName"] != expectedName) {
						t.Error("wrong create payload", n)
					}
					delete(n, "autoGenerateVlanName")
					n["id"] = "created"
					n["vlanId"] = 150
					n["vlanName"] = expectedName
					if mode == "wrong-readback" {
						n["isWirelessPool"] = true
					}
					gateways = append(gateways, n)
					if mode == "changed-pool-after-submit" {
						reservations[0].ID = "replacement"
					}
					if mode == "lost-receipt" {
						reply(map[string]string{})
					} else {
						reply(map[string]string{"taskId": "task"})
					}
				default:
					t.Error("unexpected API")
					w.WriteHeader(404)
				}
			})
			write, err := c.planAnycastGateway(ctx, want)
			if mode == "incomplete-model" {
				if err == nil || write != nil || writes != 0 {
					t.Fatal("incomplete payload admitted")
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal("planning failed", err)
			}
			if mode == "stale-before-submit" {
				reservations[0].ID = "replacement"
			}
			if mode == "name-conflict-before-submit" || mode == "id-conflict-before-submit" || mode == "zone-conflict-before-submit" || mode == "other-fabric-allocation" {
				other := anycastObservationFixture()
				other["id"] = "unrelated"
				other["ipPoolName"] = "other-pool"
				other["vlanName"] = "192.0.2.128/25-Corp"
				other["vlanId"] = 151
				if mode == "id-conflict-before-submit" || mode == "zone-conflict-before-submit" {
					other["vlanName"] = "other-name"
					other["vlanId"] = 150
				}
				if mode == "zone-conflict-before-submit" {
					other["fabricId"] = "zone"
				}
				if mode == "other-fabric-allocation" {
					other["fabricId"] = "unrelated-fabric"
				}
				gateways = append(gateways, other)
			}
			switch mode {
			case "l2-name-conflict", "l2-id-conflict", "l2-zone-conflict", "l2-other-fabric":
				row := fabricL2Network{ID: "l2", FabricID: "fabric", VLAN: 151, VLANName: "NativeL2", Traffic: "DATA", Wireless: &b, L3: "Corp"}
				if mode == "l2-id-conflict" || mode == "l2-zone-conflict" {
					row.VLAN = 150
					row.VLANName = "other"
				}
				if mode == "l2-zone-conflict" {
					row.FabricID = "zone"
				}
				if mode == "l2-other-fabric" {
					row.FabricID = "another"
				}
				layer2 = append(layer2, row)
			case "l2-unavailable":
				l2Unavailable = true
			}
			task, err := write.submit(ctx)
			if mode == "name-conflict-before-submit" || mode == "id-conflict-before-submit" || mode == "zone-conflict-before-submit" || mode == "l2-name-conflict" || mode == "l2-id-conflict" || mode == "l2-zone-conflict" || mode == "l2-unavailable" {
				if err == nil || writes != 0 {
					t.Fatal("conflicting allocation submitted", err, writes)
				}
				if mode != "l2-unavailable" && err != errConfigurationTransition {
					t.Fatal("conflict failed for wrong reason", err)
				}
				return
			}
			if mode == "stale-before-submit" {
				if err == nil || writes != 0 {
					t.Fatal("stale pool submitted")
				}
				return
			}
			if mode == "lost-receipt" {
				if err == nil || writes != 1 {
					t.Fatal("missing task accepted or replayed")
				}
				return
			}
			if err != nil || task.ID != "task" || writes != 1 {
				t.Fatal("submission failed", err)
			}
			op := &cvk.NetworkControllerConfigurationOperation{Action: write.action, Resource: write.resource, ParentID: write.identity.parent}
			verified, err := c.verifyAnycastGateway(ctx, want, op)
			valid := mode == "create" || mode == "zone" || mode == "other-fabric-allocation" || mode == "l2-other-fabric"
			if verified != valid || valid && err != nil {
				t.Fatal("incorrect completion", err)
			}
			if valid {
				next, err := c.planAnycastGateway(ctx, want)
				if err != nil || next != nil || writes != 1 {
					t.Fatal("unchanged gateway replayed", err)
				}
			}
		})
	}
}
