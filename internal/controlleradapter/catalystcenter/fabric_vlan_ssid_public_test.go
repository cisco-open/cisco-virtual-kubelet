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

func TestFabricVLANSSIDPublicLifecycle(t *testing.T) {
	for _, mode := range []string{"accepted"} {
		t.Run(mode, func(t *testing.T) {

			posts := 0
			created := false
			taskTime := time.Now()
			var afterPost func()
			a, config, _, members, _ := portChannelJournalFixture(t, "valid")
			inventory, err := a.client.configurationDevices(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			inventory[1].Family = "Wireless Controller"
			inventory[1].Reachability = "Reachable"
			inventory[1].Hostname = "controller"
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				if r.Method == "PUT" {
					if r.URL.Path != "/dna/intent/api/v1/sda/fabrics/fabric/vlanToSsids" {
						t.Error("unexpected mutation")
						w.WriteHeader(400)
						return
					}
					posts++
					created = true
					taskTime = time.Now()
					var body []fabricVLANSSID
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body[0].VLAN != "Clients" || len(body[0].SSIDs) != 1 || body[0].SSIDs[0].Name != "Staff" {
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
				case "/dna/intent/api/v1/sda/fabrics/fabric/vlanToSsids":
					rows := []fabricVLANSSID{}
					if created {
						rows = append(rows, fabricVLANSSID{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Staff"}}})
					}
					reply(rows)
				case fabricL2NetworksPath:
					b := true
					reply([]fabricL2Network{{ID: "l2", FabricID: "fabric", VLANName: "Clients", VLAN: 100, Traffic: "DATA", Wireless: &b}})
				case anycastGatewaysPath:
					reply([]any{})
				case wirelessProfilesPath:
					reply([]map[string]any{{"id": "profile", "wirelessProfileName": "Wireless", "ssidDetails": []map[string]any{{"ssidName": "Staff", "enableFabric": true}}}})
				case profileSiteAssignmentsPath("profile"):
					reply([]map[string]string{{"id": "area"}})
				case wirelessSSIDPath("area"), wirelessSSIDPath("root"):
					reply([]map[string]any{{"id": "ssid", "ssid": "Staff", "authType": "OPEN", "wlanType": "Enterprise"}})
				case "/dna/intent/api/v1/wirelessControllers/peer-id/provisionStatus":
					reply(map[string]any{"networkDeviceId": "peer-id", "deviceName": "controller", "siteId": "area", "siteNameHierarchy": "Global/Lab", "networkIntentProvisionStatus": "SUCCESS", "modelConfigProvisionStatus": "SUCCESS", "lastProvisionedTimeStamp": "12345", "outOfSync": false})
				case taskPath + "task/tree":
					end := taskTime.UnixMilli()
					if mode == "running" {
						end = 0
					}
					reply([]map[string]any{{"id": "task", "isError": mode == "failed", "startTime": taskTime.Add(-time.Second).UnixMilli(), "endTime": end}})

				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			ctx := context.Background()
			config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"fabric":{"fabric_sites":[{"name":"Global/Lab","pub_sub_enabled":false,"authentication_template":{"name":"No Authentication"},"wireless_ssids":[{"name":"Staff","vlan_name":"Clients"}]}]}}}`)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}

			testPublicHandoffPreparation(t, a, config)
			configurationGroupDispatchFixture(t, a, members, false)
			installFabricGroupAuthorityFixture(t, a)
			testPublicHandoffDispatch(t, a, config)
			a.configurationAdmission = nil
			var list ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &list); err != nil {
				t.Fatal(err)
			}
			claims := make([]*ops.ControllerConfigurationReservation, len(list.Items))
			for i := range list.Items {
				claims[i] = list.Items[i].DeepCopy()
			}
			testPublicHandoffRecovery(t, a, config, claims, "dispatch-recovery-public")
			if posts != 1 || !created {
				t.Fatal("public mutation count/state", posts, created)
			}
		})
	}
}
