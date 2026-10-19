// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestFabricWirelessMulticastDispatch(t *testing.T) {
	for _, mode := range []string{"accepted", "denied", "lost", "revoked", "missing-permit"} {
		t.Run(mode, func(t *testing.T) {
			posts := 0
			created := false
			var afterPost func()
			a, config, _, members, _ := portChannelJournalFixture(t, "valid")
			inventory, err := a.client.configurationDevices(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				if r.Method == "PUT" {
					if r.URL.Path != "/dna/intent/api/v1/sda/fabrics/fabric/wirelessMulticast" {
						t.Error("unexpected mutation")
						w.WriteHeader(400)
						return
					}
					posts++
					created = true
					var body map[string]bool
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || !body["multicastEnabled"] {
						t.Error("wrong native payload")
					}
					if afterPost != nil {
						afterPost()
					}
					if mode == "lost" {
						w.WriteHeader(500)
						return
					}
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected method")
					w.WriteHeader(400)
					return
				}
				switch r.URL.Path {
				case sitesPath:
					reply(sampleSites())
				case fabricSitesPath:
					b := false
					reply([]fabricSite{{ID: "fabric", SiteID: "area", Authentication: "No Authentication", PubSub: &b}})
				case devicesPath:
					reply(inventory)
				case fabricDevicesPath:
					reply([]fabricDeviceMembership{{ID: "edge-member", NetworkDeviceID: "edge-id", FabricID: "fabric", Roles: []string{"EDGE_NODE"}}, {ID: "wlc-member", NetworkDeviceID: "peer-id", FabricID: "fabric", Roles: []string{"WIRELESS_CONTROLLER_NODE"}}})
				case "/dna/intent/api/v1/sda/fabrics/fabric/wirelessMulticast":
					reply(map[string]bool{"multicastEnabled": created})
				case "/dna/intent/api/v1/sda/fabrics/fabric/vlanToSsids":
					reply([]fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Staff"}}}})
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			ctx := context.Background()
			config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"fabric":{"fabric_sites":[{"name":"Global/Lab","pub_sub_enabled":false,"authentication_template":{"name":"No Authentication"},"multicast":{"wireless_multicast_enabled":true}}]}}}`)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := a.prepareFabricWirelessMulticast(ctx, config, source, "Global/Lab"); err != nil {
				t.Fatal(err)
			}
			permit, revoke := configurationGroupDispatchFixture(t, a, members, true)
			if mode == "denied" {
				revoke()
			}
			if mode == "revoked" {
				afterPost = revoke
			}
			if mode == "missing-permit" {
				permit = nil
			}
			task, err := a.dispatchFabricWirelessMulticast(ctx, config, source, "Global/Lab", permit)
			if (err == nil) != (mode == "accepted") {
				t.Fatal("dispatch outcome", err)
			}
			state := journalState(t, a).Operation
			switch mode {
			case "accepted":
				if state.Phase != "Accepted" || state.TaskID != "task" || task.ID != "task" {
					t.Fatal("accepted task not retained")
				}
			case "denied", "missing-permit":
				if state.Phase != "Prepared" || state.SubmittedAt != nil || posts != 0 {
					t.Fatal("unauthorized dispatch advanced")
				}
			default:
				if state.Phase != "Submitting" || state.TaskID != "" {
					t.Fatal("uncertain submission was cleared")
				}
			}
			if mode == "revoked" && task.ID != "task" {
				t.Fatal("known task lost on receipt refusal")
			}
			expected := 1
			if mode == "denied" || mode == "missing-permit" {
				expected = 0
			}
			if posts != expected {
				t.Fatal("dispatch count", posts)
			}
			restarted := *a
			if _, err := restarted.dispatchFabricWirelessMulticast(ctx, config, source, "Global/Lab", permit); err == nil || posts != expected {
				t.Fatal("restart replayed dispatch")
			}
		})
	}
}
