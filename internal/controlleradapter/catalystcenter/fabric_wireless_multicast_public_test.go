// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"net/http"
	"testing"
	"time"
)

func TestFabricWirelessMulticastPublicLifecycle(t *testing.T) {
	for _, mode := range []string{"enable", "disable"} {
		t.Run(mode, func(t *testing.T) {
			posts := 0
			desired := mode == "enable"
			created := !desired
			taskTime := time.Now()
			a, config, _, physical, _ := portChannelJournalFixture(t, "valid")
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
					created = desired
					var body map[string]bool
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["multicastEnabled"] != desired {
						t.Error("wrong native payload")
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
				case taskPath + "task/tree":
					end := taskTime.UnixMilli()
					reply([]map[string]any{{"id": "task", "isError": false, "startTime": taskTime.Add(-time.Second).UnixMilli(), "endTime": taskTime.UnixMilli()}, {"id": "child", "parentId": "task", "rootId": "task", "isError": false, "startTime": taskTime.Add(-time.Second).UnixMilli(), "endTime": end}})

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
					name := "Staff"
					reply([]fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: name}}}})
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			ctx := context.Background()
			config.Spec.Source.Inline.Raw = []byte(fmt.Sprintf(`{"catalyst_center":{"fabric":{"fabric_sites":[{"name":"Global/Lab","pub_sub_enabled":false,"authentication_template":{"name":"No Authentication"},"multicast":{"wireless_multicast_enabled":%t}}]}}}`, desired))
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}

			testPublicHandoffPreparation(t, a, config)
			configurationGroupDispatchFixture(t, a, physical, false)
			installFabricGroupAuthorityFixture(t, a)
			testPublicHandoffDispatch(t, a, config)
			a.configurationAdmission = nil
			var list ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &list); err != nil {
				t.Fatal(err)
			}
			members := make([]*ops.ControllerConfigurationReservation, len(list.Items))
			for i := range list.Items {
				members[i] = list.Items[i].DeepCopy()
			}
			testPublicHandoffRecovery(t, a, config, members, "dispatch-recovery-public")
			if posts != 1 || created != desired {
				t.Fatal("public lifecycle mutation count/state", posts, created)
			}
		})
	}
}
