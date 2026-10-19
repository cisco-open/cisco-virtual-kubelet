// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"net/http"
	"testing"
	"time"
)

func TestFabricWirelessMulticastRecovery(t *testing.T) {
	for _, mode := range []string{"valid", "unknown", "running", "failed", "missing", "replaced", "denied", "no-task", "missing-member", "source", "owner", "publish-lost", "publish-denied", "publish-incomplete", "publish-changing"} {
		t.Run(mode, func(t *testing.T) {
			posts := 0
			created := false
			taskTime := time.Now()
			publishing := false
			publicationReads := 0
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
					created = true
					var body map[string]bool
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || !body["multicastEnabled"] {
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
					if mode == "running" {
						end = 0
					}
					reply([]map[string]any{{"id": "task", "isError": false, "startTime": taskTime.Add(-time.Second).UnixMilli(), "endTime": taskTime.UnixMilli()}, {"id": "child", "parentId": "task", "rootId": "task", "isError": mode == "failed", "startTime": taskTime.Add(-time.Second).UnixMilli(), "endTime": end}})

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
					enabled := created && mode != "missing"
					if publishing && mode == "publish-changing" {
						publicationReads++
						enabled = publicationReads%2 == 1
					}
					reply(map[string]bool{"multicastEnabled": enabled})
				case "/dna/intent/api/v1/sda/fabrics/fabric/vlanToSsids":
					name := "Staff"
					if created && mode == "replaced" {
						name = "Other"
					}
					reply([]fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: name}}}})
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
			permit, _ := configurationGroupDispatchFixture(t, a, physical, true)
			if _, err := a.dispatchFabricWirelessMulticast(ctx, config, source, "Global/Lab", permit); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			expected := "Accepted"
			switch mode {
			case "unknown":
				nc.Status.Configuration.Operation.Phase = "Unknown"
				expected = "Unknown"
			case "no-task":
				nc.Status.Configuration.Operation.TaskID = ""
			case "owner":
				nc.Status.Configuration.Owners = nil
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			var list ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &list); err != nil {
				t.Fatal(err)
			}
			members := make([]*ops.ControllerConfigurationReservation, len(list.Items))
			for i := range list.Items {
				members[i] = list.Items[i].DeepCopy()
			}
			if mode == "missing-member" {
				if err := a.statusWriter.Delete(ctx, members[0]); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "source" {
				changed := config.DeepCopy()
				changed.Generation++
				if err := a.statusWriter.Update(ctx, changed); err != nil {
					t.Fatal(err)
				}
			}
			var beforeRecovery ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &beforeRecovery); err != nil {
				t.Fatal(err)
			}
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error {
				if mode == "denied" {
					return errors.New("fixture denied")
				}
				return nil
			}
			restarted := *a
			err = restarted.beginFabricWirelessMulticastVerification(ctx, config, source, "Global/Lab", members, authorize, time.Now())
			successful := mode == "valid" || mode == "unknown" || mode == "publish-lost" || mode == "publish-denied" || mode == "publish-incomplete" || mode == "publish-changing"
			if (err == nil) != successful {
				t.Fatal("verification outcome", err)
			}
			if successful {
				expected = "Verifying"
			}
			state := journalState(t, a)
			if state.Operation.Phase != expected || len(state.ActionApplications) != 0 {
				t.Fatal("verification released/cleared work")
			}
			if posts != 1 {
				t.Fatal("native replay", posts)
			}
			var retained ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &retained); err != nil {
				t.Fatal(err)
			}
			if len(retained.Items) != len(beforeRecovery.Items) {
				t.Fatal("verification removed claims")
			}

			if !successful {
				return
			}
			originalWriter := restarted.statusWriter
			publishing = true
			if mode == "publish-lost" {
				restarted.statusWriter = lostJournalAckClient{originalWriter}
			}
			receiptMembers := members
			if mode == "publish-incomplete" {
				receiptMembers = nil
			}
			err = restarted.recordFabricWirelessMulticastApplication(ctx, config, state.Operation.ID, "Global/Lab", receiptMembers, func(context.Context, []*ops.ControllerConfigurationReservation) error {
				if mode == "publish-denied" {
					return errors.New("fixture denied receipt")
				}
				return nil
			}, time.Now())
			published := mode != "publish-denied" && mode != "publish-incomplete" && mode != "publish-changing"
			if (err == nil) != (published && mode != "publish-lost") {
				t.Fatal("receipt outcome", err)
			}
			state = journalState(t, a)
			if published {
				if state.Operation.Phase != "Verified" || len(state.ActionApplications) != 1 {
					t.Fatal("non-atomic verified receipt")
				}
				nc, err = a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := contract.GroupCompletionsForReceipt(nc, members, time.Now()); err != nil {
					t.Fatal("receipt cannot derive completion", err)
				}
			} else if state.Operation.Phase != "Verifying" || len(state.ActionApplications) != 0 {
				t.Fatal("denied publication changed state")
			}
			if err := a.statusReader.List(ctx, &list); err != nil {
				t.Fatal(err)
			}
			for _, member := range list.Items {
				if member.Status.Completion != nil || member.Status.Grant.Phase != "Held" || member.Status.ManagerSettledAt != nil {
					t.Fatal("verification released member")
				}
			}
			restarted.statusWriter = originalWriter
			if published {
				if err := restarted.recordFabricWirelessMulticastApplication(ctx, config, state.Operation.ID, "Global/Lab", members, authorize, time.Now()); err == nil {
					t.Fatal("receipt publication replayed")
				}
				// Reuse the existing adapter terminal fixture, including lost completion
				// acknowledgment, source deletion and partial release/manager settlement.
				testFabricHandoffCompletion(t, &restarted, config)
			}
			if posts != 1 {
				t.Fatal("settlement replayed native work")
			}
		})
	}
}
