// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"net/http"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
)

func TestMulticastVNManagedTargets(t *testing.T) {
	for _, mode := range []string{"valid", "missing-managed-peer", "replaced-node", "late-serial", "late-pool", "transient-created-target"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, members, _ := portChannelJournalFixture(t, "valid")
			inventory, err := a.client.configurationDevices(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "missing-managed-peer" {
				if err := a.statusWriter.Delete(ctx, members[1].device); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "replaced-node" {
				node := members[1].node
				if err := a.statusWriter.Delete(ctx, node); err != nil {
					t.Fatal(err)
				}
				node.UID = "replacement"
				node.ResourceVersion = ""
				if err := a.statusWriter.Create(ctx, node); err != nil {
					t.Fatal(err)
				}
			}
			inventory[0].Hostname = "border-a.lab"
			inventory[1].Hostname = "edge-peer.lab"
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("preparation mutated appliance")
					w.WriteHeader(400)
					return
				}
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				switch r.URL.Path {
				case sitesPath:
					reply(sampleSites())
				case fabricSitesPath:
					b := false
					reply([]fabricSite{{ID: "fabric", SiteID: "area", Authentication: "No Authentication", PubSub: &b}})
				case fabricL3NetworksPath:
					reply([]fabricL3Network{{ID: "vn", Name: "Corp", Fabrics: []string{"fabric"}}})
				case fabricMulticastVNsPath:
					if mode == "transient-created-target" && reads == 2 {
						reply([]map[string]any{multicastSiteRow(t, "Corp", "access", "edge-id").fields})
						return
					}
					reply([]any{})
				case globalPoolsPath:
					reply(fixturePools())
				case sitePoolsPath:
					rows := fixtureReservations()
					if mode == "late-pool" && reads > 0 {
						rows[0].ID = "replacement"
					}
					reply(rows)
				case devicesPath:
					reads++
					rows := append([]Device(nil), inventory...)
					if mode == "late-serial" && reads > 1 {
						rows[1].Serial = "FOC99999999"
					}
					reply(rows)
				case fabricDevicesPath:
					reply([]fabricDeviceMembership{{ID: "border", NetworkDeviceID: "edge-id", FabricID: "fabric", Roles: []string{"BORDER_NODE"}}, {ID: "edge", NetworkDeviceID: "peer-id", FabricID: "fabric", Roles: []string{"EDGE_NODE"}}})
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			want, err := parseFabricMulticastVN(map[string]any{"name": "Corp", "ip_pool_name": "access", "ipv4_ssm_ranges": []any{"232.0.0.0/8"}, "multicast_rps": []any{map[string]any{"rp_location": "FABRIC", "fabric_rps": []any{"border-a"}}}})
			if err != nil {
				t.Fatal(err)
			}
			write, targets, err := c.planManagedFabricMulticastVN(ctx, a.statusReader, config.Namespace, "Global/Lab", []fabricMulticastVNIntent{want}, "Corp")
			if mode != "valid" {
				if err == nil || write != nil || targets != nil {
					t.Fatal("partial or unstable scope accepted", err)
				}
				return
			}
			if err != nil || write == nil || len(targets) != 2 {
				t.Fatal("non-RP footprint missing", targets, err)
			}
			if _, err := write.submit(ctx); !errors.Is(err, errConfigurationDeviceAdmission) {
				t.Fatal("preparation acquired authority", err)
			}
			var reservations ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &reservations); err != nil || len(reservations.Items) != 0 {
				t.Fatal("preparation created claims", err)
			}
			for _, member := range members {
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(member.node), member.node); err != nil || member.node.Spec.Unschedulable {
					t.Fatal("preparation cordoned node", err)
				}
			}
		})
	}
}

func TestMulticastVNGroupResource(t *testing.T) {
	good := strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64) + ":" + strings.Repeat("c", 64)
	for _, suffix := range []string{good, "", "pruned", strings.Repeat("a", 64), strings.ToUpper(good), good + ":extra", strings.Repeat("g", 64) + ":" + strings.Repeat("b", 64)} {
		resource := "fabric-multicast-vn:" + suffix
		if !isReservedConfigurationGroupResource("fabric", resource) {
			t.Fatal("lost group prefix reservation")
		}
		for _, action := range []string{"Create", "Update", "Delete"} {
			expected := suffix == good && action != "Delete"
			if validConfigurationGroupAction("fabric", resource, action) != expected {
				t.Fatal("group classification", suffix, action)
			}
		}
	}
	if validConfigurationGroupAction("inventory", "fabric-multicast-vn:"+good, "Update") {
		t.Fatal("wrong section accepted")
	}
}
