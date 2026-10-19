// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"strings"
	"testing"
)

func TestAnycastExecutorParentOrdering(t *testing.T) {
	for _, mode := range []string{"create", "missing-parent-declaration", "incomplete-child", "missing-zone-membership", "replaced-parent", "bulk-create", "bulk-incomplete-child", "bulk-replaced-parent"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			bulk := strings.HasPrefix(mode, "bulk-")
			mode = strings.TrimPrefix(mode, "bulk-")
			b := false
			gateways := []map[string]any{}
			writes := 0
			model := map[string]any{"ip_pool_name": "access", "l3_virtual_network": "Corp", "traffic_type": "DATA", "auto_generate_vlan_name": true, "critical_pool": false, "layer2_flooding": false, "wireless_pool": false, "ip_directed_broadcast": false, "intra_subnet_routing_enabled": false, "multiple_ip_to_mac_addresses": false}
			parent, err := parseAnycastGateway(model)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "incomplete-child" {
				delete(model, "wireless_pool")
			}
			child, err := parseAnycastGateway(model)
			if err != nil {
				t.Fatal(err)
			}
			// Deliberately reverse intent order: dependency ordering must be semantic.
			desired := []fabricAnycastIntent{{path: "Global/Lab/Building", parent: "Global/Lab", anycastGatewayIntent: child}, {path: "Global/Lab", anycastGatewayIntent: parent}}
			if mode == "missing-parent-declaration" {
				desired = desired[:1]
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
					members := []string{"fabric", "zone"}
					if mode == "missing-zone-membership" {
						members = members[:1]
					}
					reply([]fabricL3Network{{ID: "network", Name: "Corp", Fabrics: members}})
				case globalPoolsPath:
					reply(fixturePools())
				case sitePoolsPath:
					reply(fixtureReservations())
				case fabricL2NetworksPath:
					reply([]fabricL2Network{})
				case anycastGatewaysPath:
					if r.Method == "GET" {
						reply(gateways)
						return
					}
					if r.Method != "POST" {
						t.Error("unexpected mutation")
						w.WriteHeader(400)
						return
					}
					var rows []map[string]any
					if json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 {
						t.Error("bad payload")
						w.WriteHeader(400)
						return
					}
					target := "fabric"
					if writes == 1 {
						target = "zone"
						if rows[0]["vlanId"] != float64(150) {
							t.Error("lost parent allocation")
						}
					}
					if rows[0]["fabricId"] != target {
						t.Error("child preceded parent")
					}
					n := rows[0]
					delete(n, "autoGenerateVlanName")
					n["id"] = target + "-gateway"
					n["vlanId"] = 150
					n["vlanName"] = "192.0.2.128/25-Corp"
					gateways = append(gateways, n)
					writes++
					if writes == 2 && mode == "replaced-parent" {
						gateways[0]["id"] = "replacement"
					}
					reply(map[string]string{"taskId": fmt.Sprintf("task-%d", writes)})
				default:
					t.Error("unexpected API")
					w.WriteHeader(404)
				}
			})
			for i := 0; i < 2; i++ {
				e := &anycastGatewayExecutor{client: c, desired: desired}
				if bulk {
					e.bulkGuard = func(context.Context, string, string) error { return nil }
				}
				if i == 0 && mode == "create" {
					drift, err := e.report(ctx)
					if err != nil || len(drift) != 2 || writes != 0 {
						t.Fatal("pending parent report failed", err)
					}
				}
				write, err := e.next(ctx)
				invalid := mode == "missing-parent-declaration" || mode == "incomplete-child" || mode == "missing-zone-membership"
				if invalid {
					if err == nil || write != nil || writes != 0 {
						t.Fatal("invalid child allowed parent write")
					}
					return
				}
				if err != nil || write == nil {
					t.Fatal("planning failed", err)
				}
				if _, err := write.submit(ctx); err != nil {
					t.Fatal(err)
				}
				op := &cvk.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, ParentID: write.identity.parent}
				restarted := &anycastGatewayExecutor{client: c, desired: desired}
				verified, err := restarted.verify(ctx, op)
				if i == 1 && mode == "replaced-parent" {
					if verified || err == nil {
						t.Fatal("replaced parent verified")
					}
					return
				}
				if !verified || err != nil {
					t.Fatal("verification failed", err)
				}
			}
			e := &anycastGatewayExecutor{client: c, desired: desired}
			if bulk {
				e.bulkGuard = func(context.Context, string, string) error { return nil }
			}
			write, err := e.next(ctx)
			if err != nil || write != nil || writes != 2 {
				t.Fatal("converged gateway replayed", err)
			}
		})
	}
}
