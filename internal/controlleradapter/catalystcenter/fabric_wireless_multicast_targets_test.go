// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"net/http"
	"reflect"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
)

func TestFabricWirelessMulticastManagedTargets(t *testing.T) {
	for _, mode := range []string{"valid", "missing-managed", "replaced-node", "missing-native", "duplicate-serial", "late-serial", "late-mapping"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, members, _ := portChannelJournalFixture(t, "valid")
			inventory, err := a.client.configurationDevices(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "missing-managed" {
				if err := a.statusWriter.Delete(ctx, members[1].device); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "replaced-node" {
				node := members[1].node
				if err := a.statusWriter.Delete(ctx, node); err != nil {
					t.Fatal(err)
				}
				node.ResourceVersion = ""
				node.UID = "replacement"
				if err := a.statusWriter.Create(ctx, node); err != nil {
					t.Fatal(err)
				}
			}
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("observation mutated controller")
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
				case fabricDevicesPath:
					reply([]fabricDeviceMembership{{ID: "edge-member", NetworkDeviceID: "edge-id", FabricID: "fabric", Roles: []string{"EDGE_NODE"}}, {ID: "wlc-member", NetworkDeviceID: "peer-id", FabricID: "fabric", Roles: []string{"WIRELESS_CONTROLLER_NODE"}}})
				case "/dna/intent/api/v1/sda/fabrics/fabric/wirelessMulticast":
					reply(map[string]bool{"multicastEnabled": false})
				case "/dna/intent/api/v1/sda/fabrics/fabric/vlanToSsids":
					name := "Staff"
					if mode == "late-mapping" && reads > 0 {
						name = "Other"
					}
					reply([]fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: name}}}})
				case devicesPath:
					reads++
					rows := append([]Device(nil), inventory...)
					if mode == "missing-native" {
						rows = rows[:1]
					}
					if mode == "duplicate-serial" {
						rows[1].Serial = rows[0].Serial
					}
					if mode == "late-serial" && reads > 1 {
						rows[1].Serial = "FOC99999999"
					}
					reply(rows)
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			state, targets, err := c.wirelessMulticastTargets(ctx, a.statusReader, config.Namespace, "Global/Lab")
			if mode != "valid" {
				if err == nil || state != nil || targets != nil {
					t.Fatal("partial or stale scope admitted", err)
				}
				return
			}
			if err != nil || state == nil || len(state.physical) != 2 || len(targets) != 2 || reads != 2 {
				t.Fatal("managed scope", err, targets, reads)
			}
			write, plannedTargets, err := c.planManagedFabricWirelessMulticast(ctx, a.statusReader, config.Namespace, "Global/Lab", true)
			if err != nil || write == nil || !reflect.DeepEqual(targets, plannedTargets) {
				t.Fatal("managed plan", err)
			}
			if _, err := write.submit(ctx); !errors.Is(err, errConfigurationDeviceAdmission) {
				t.Fatal("preparation obtained submission authority", err)
			}
			noop, _, err := c.planManagedFabricWirelessMulticast(ctx, a.statusReader, config.Namespace, "Global/Lab", false)
			if err != nil || noop != nil {
				t.Fatal("matching state planned mutation", err)
			}
			a.client = c
			config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"fabric":{"fabric_sites":[{"name":"Global/Lab","pub_sub_enabled":false,"authentication_template":{"name":"No Authentication"},"multicast":{"wireless_multicast_enabled":true}}]}}}`)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.prepareFabricWirelessMulticast(ctx, config, source, "Global/Other"); err == nil {
				t.Fatal("undeclared target admitted")
			}
			op, err := a.prepareFabricWirelessMulticast(ctx, config, source, "Global/Lab")
			if err != nil || op == nil || op.Phase != "Prepared" || contract.ValidateGroupJournalIdentity(op) != nil || len(op.PhysicalGroup.Targets) != 2 {
				t.Fatal("durable preparation", err, op)
			}
			if err := a.revalidateFabricWirelessMulticastOperation(ctx, config, source, "Global/Lab", op, "Prepared"); err != nil {
				t.Fatal("revalidate", err)
			}
			if _, err := a.prepareFabricWirelessMulticast(ctx, config, source, "Global/Lab"); err == nil {
				t.Fatal("active journal replaced")
			}
			var reservations ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &reservations); err != nil || len(reservations.Items) != 0 {
				t.Fatal("preparation created reservations", err)
			}
			for _, member := range members {
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(member.node), member.node); err != nil || member.node.Spec.Unschedulable {
					t.Fatal("target observation cordoned node", err)
				}
			}
		})
	}
}
