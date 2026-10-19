// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestAnycastGatewayBulkNativeLifecycle(t *testing.T) {
	for _, mode := range []string{"create", "existing-member", "noop", "partial", "lost", "denied", "missing-guard", "duplicate", "pending-vlan-conflict", "late-pool", "guard-pool-race", "l2-conflict", "replacement-readback", "wrong-readback", "changed-intent", "malformed-mask", "pending-name-conflict", "unresolved-later-model", "baseline-replaced-before-submit", "caller-payload-mutation", "existing-member-omitted-field", "mixed-fabric", "executor-create", "executor-partial", "executor-waiting-invalid-existing"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			reservations := fixtureReservations()
			second := reservations[0]
			second.ID, second.Name = "reservation-b", "access-b"
			v4 := *second.V4
			v4.Subnet = "192.0.2.0"
			gateway := "192.0.2.1"
			v4.Gateway = &gateway
			second.V4 = &v4
			reservations = append(reservations, second)
			wants := []fabricAnycastIntent{}
			for _, pool := range []string{"access-b", "access"} {
				model := map[string]any{"ip_pool_name": pool, "l3_virtual_network": "Corp", "traffic_type": "DATA", "auto_generate_vlan_name": true, "critical_pool": false, "layer2_flooding": false, "wireless_pool": false, "ip_directed_broadcast": false, "intra_subnet_routing_enabled": false, "multiple_ip_to_mac_addresses": false}
				if mode == "pending-name-conflict" {
					model["auto_generate_vlan_name"] = false
					model["vlan_name"] = "sharedVlan"
				}
				if mode == "pending-vlan-conflict" {
					model["vlan_id"] = 150
				}
				intent, err := parseAnycastGateway(model)
				if err != nil {
					t.Fatal(err)
				}
				wants = append(wants, fabricAnycastIntent{path: "Global/Lab", anycastGatewayIntent: intent})
			}
			if mode == "unresolved-later-model" {
				delete(wants[1].payload, "isWirelessPool")
			}
			if mode == "executor-waiting-invalid-existing" {
				zoneA, zoneB := wants[1], wants[0]
				zoneA.path, zoneA.parent = "Global/Lab/Building", "Global/Lab"
				zoneB.path, zoneB.parent = "Global/Lab/Building", "Global/Lab"
				wants = append(wants, zoneA, zoneB)
			}
			if mode == "mixed-fabric" {
				wants[1].path, wants[1].parent = "Global/Lab/Building", "Global/Lab"
			}
			if mode == "duplicate" {
				wants[1] = wants[0]
			}
			gateways := []map[string]any{}
			appendGateway := func(body map[string]any) {
				row := map[string]any{}
				for k, v := range body {
					if k != "autoGenerateVlanName" {
						row[k] = v
					}
				}
				pool := row["ipPoolName"].(string)
				row["id"] = "gateway-" + pool
				row["vlanId"] = 150 + len(gateways)
				row["vlanName"] = "192.0.2.128/25-Corp"
				if pool == "access-b" {
					row["vlanName"] = "192.0.2.0/25-Corp"
				}
				gateways = append(gateways, row)
			}
			if mode == "existing-member" || mode == "noop" || mode == "replacement-readback" || mode == "baseline-replaced-before-submit" || mode == "existing-member-omitted-field" || mode == "mixed-fabric" || mode == "executor-waiting-invalid-existing" {
				body := map[string]any{}
				for k, v := range wants[1].payload {
					body[k] = v
				}
				body["fabricId"] = "fabric"
				appendGateway(body)
			}
			if mode == "executor-waiting-invalid-existing" {
				body := map[string]any{}
				for k, v := range wants[1].payload {
					body[k] = v
				}
				body["fabricId"] = "zone"
				body["isWirelessPool"] = true
				appendGateway(body)
				gateways[len(gateways)-1]["vlanId"] = 150
			}
			if mode == "noop" {
				body := map[string]any{}
				for k, v := range wants[0].payload {
					body[k] = v
				}
				body["fabricId"] = "fabric"
				appendGateway(body)
			}
			if mode == "existing-member-omitted-field" {
				delete(wants[1].payload, "isWirelessPool")
			}
			writes := 0
			payloadCount := 0
			flag := false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case sitesPath:
					reply(sampleSites())
				case fabricSitesPath:
					reply([]fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &flag}})
				case fabricZonesPath:
					if mode == "mixed-fabric" || mode == "executor-waiting-invalid-existing" {
						reply([]fabricZone{{ID: "zone", SiteID: "building", Authentication: "No Authentication"}})
					} else {
						reply([]fabricZone{})
					}
				case fabricL3NetworksPath:
					fabrics := []string{"fabric"}
					if mode == "mixed-fabric" || mode == "executor-waiting-invalid-existing" {
						fabrics = append(fabrics, "zone")
					}
					reply([]fabricL3Network{{ID: "network", Name: "Corp", Fabrics: fabrics}})
				case globalPoolsPath:
					reply(fixturePools())
				case sitePoolsPath:
					reply(reservations)
				case fabricL2NetworksPath:
					rows := []fabricL2Network{}
					if mode == "l2-conflict" {
						rows = append(rows, fabricL2Network{ID: "l2", FabricID: "fabric", VLAN: 200, VLANName: "192.0.2.0/25-Corp"})
					}
					reply(rows)
				case anycastGatewaysPath:
					if r.Method == "GET" {
						reply(gateways)
						return
					}
					if r.Method != "POST" {
						t.Error("unexpected mutation")
						w.WriteHeader(500)
						return
					}
					writes++
					var body []map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Error("bad bulk body")
					}
					payloadCount = len(body)
					for i, row := range body {
						if (mode != "partial" && mode != "executor-partial") || i == 0 {
							appendGateway(row)
						}
					}
					if mode == "lost" {
						reply(map[string]any{})
						return
					}
					reply(map[string]any{"taskId": "bulk-task"})
				default:
					t.Errorf("unexpected API %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			guardCalls := 0
			guard := func(context.Context) error {
				guardCalls++
				if mode == "denied" {
					return errors.New("denied")
				}
				if mode == "guard-pool-race" {
					reservations[1].ID = "replacement"
				}
				return nil
			}
			if mode == "missing-guard" {
				guard = nil
			}
			var executor *anycastGatewayExecutor
			var write *configurationWrite
			var err error
			if mode == "executor-create" || mode == "executor-partial" || mode == "executor-waiting-invalid-existing" {
				executor = &anycastGatewayExecutor{client: c, desired: wants, bulkGuard: func(ctx context.Context, _ string, _ string) error { return guard(ctx) }}
				write, err = executor.next(ctx)
			} else {
				write, err = c.planAnycastGatewayBulk(ctx, wants, guard)
			}
			invalid := mode == "missing-guard" || mode == "duplicate" || mode == "pending-vlan-conflict" || mode == "l2-conflict" || mode == "pending-name-conflict" || mode == "unresolved-later-model" || mode == "mixed-fabric" || mode == "executor-waiting-invalid-existing"
			if invalid {
				if err == nil || write != nil || writes != 0 {
					t.Fatal("invalid batch planned", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "noop" {
				if write != nil || writes != 0 || guardCalls != 0 {
					t.Fatal("converged batch replayed")
				}
				return
			}
			if write == nil {
				t.Fatal("missing batch")
			}
			if mode == "baseline-replaced-before-submit" {
				gateways[0]["id"] = "replaced-existing"
			}
			if mode == "caller-payload-mutation" {
				wants[0].payload["isWirelessPool"] = true
			}
			if mode == "late-pool" {
				reservations[1].ID = "replacement"
			}
			task, err := write.submit(ctx)
			refused := mode == "denied" || mode == "late-pool" || mode == "guard-pool-race" || mode == "baseline-replaced-before-submit"
			if refused || mode == "lost" {
				expected := 0
				if mode == "lost" {
					expected = 1
				}
				if err == nil || writes != expected {
					t.Fatal("unsafe/uncertain submit outcome", err, writes)
				}
				if _, err := write.submit(ctx); err == nil || writes != expected {
					t.Fatal("uncertain/refused batch replayed")
				}
				return
			}
			if err != nil || task.ID != "bulk-task" || writes != 1 || guardCalls != 1 {
				t.Fatal("bulk submit", err)
			}
			expected := 2
			if mode == "existing-member" || mode == "replacement-readback" || mode == "baseline-replaced-before-submit" || mode == "existing-member-omitted-field" {
				expected = 1
			}
			if payloadCount != expected {
				t.Fatal("converged member included in native payload")
			}
			if mode == "caller-payload-mutation" {
				for _, row := range gateways {
					if row["isWirelessPool"] != false {
						t.Fatal("caller mutation changed captured payload")
					}
				}
				wants[0].payload["isWirelessPool"] = false
			}
			op := &cvk.NetworkControllerConfigurationOperation{Action: "Create", Resource: write.resource, ParentID: write.identity.parent}
			if mode == "replacement-readback" {
				gateways[0]["id"] = "replaced-existing"
			}
			if mode == "wrong-readback" {
				gateways[0]["isWirelessPool"] = true
			}
			if mode == "changed-intent" {
				wants[0].payload["isWirelessPool"] = true
			}
			if mode == "malformed-mask" {
				op.Resource = anycastBulkPrefix + "2:ff:bad"
			}
			done, err := c.verifyAnycastGatewayBulk(ctx, wants, op)
			if executor != nil {
				restarted := &anycastGatewayExecutor{client: c, desired: wants}
				done, err = restarted.verify(ctx, op)
			}
			valid := mode == "create" || mode == "existing-member" || mode == "caller-payload-mutation" || mode == "existing-member-omitted-field" || mode == "executor-create"
			if done != valid || (valid || mode == "partial" || mode == "executor-partial") && err != nil {
				t.Fatal("batch verification", done, err)
			}
			if _, err := write.submit(ctx); err == nil || writes != 1 {
				t.Fatal("successful batch replayed")
			}
		})
	}
}
