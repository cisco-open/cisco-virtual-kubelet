// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"net/http"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
)

func TestFabricEWLCManagedTargets(t *testing.T) {
	for _, mode := range []string{"valid", "missing-managed", "replaced-node", "missing-native", "duplicate-serial", "late-serial", "late-settings"} {
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
					reply([]fabricDeviceMembership{{ID: "edge-member", NetworkDeviceID: "edge-id", FabricID: "fabric", Roles: []string{"EDGE_NODE"}}, {ID: "wlc-member", NetworkDeviceID: "peer-id", FabricID: "fabric", Roles: []string{"EDGE_NODE"}}})
				case "/dna/intent/api/v1/sda/fabrics/fabric/switchWirelessSetting":
					percentage := 25
					if mode == "late-settings" && reads > 0 {
						percentage = 15
					}
					reply([]fabricEWLCSetting{{ID: "edge-id", Enabled: true, Rolling: fabricEWLCRolling{Percentage: percentage}}})
				case devicesPath:
					reads++
					rows := append([]Device(nil), inventory...)
					for i := range rows {
						rows[i].Family = "Switches and Hubs"
						rows[i].Reachability = "Reachable"
					}
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
			state, targets, err := c.fabricEWLCTargets(ctx, a.statusReader, config.Namespace, "fabric")
			if mode != "valid" {
				if err == nil || state != nil || targets != nil {
					t.Fatal("partial or stale scope admitted", err)
				}
				return
			}
			if err != nil || state == nil || len(state.physical) != 2 || len(targets) != 2 || reads != 2 {
				t.Fatal("managed scope", err, targets, reads)
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
