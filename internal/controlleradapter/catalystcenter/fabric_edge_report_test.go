// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestFabricEdgeReport(t *testing.T) {
	for _, mode := range []string{"matching", "missing", "channel-conflict", "assignment-conflict", "last-incomplete"} {
		t.Run(mode, func(t *testing.T) {
			a, config, _, members, _ := portChannelJournalFixture(t, "edge-mixed", func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("report mutation")
					w.WriteHeader(400)
					return
				}
				var rows any
				if r.URL.Path == fabricPortChannelsPath {
					channels := []fabricPortChannel{}
					if mode != "missing" {
						protocol := "LACP"
						if mode == "channel-conflict" {
							protocol = "ON"
						}
						channels = append(channels, fabricPortChannel{ID: "channel", FabricID: "fabric", DeviceID: "edge-id", Name: "Port-channel99", Interfaces: []string{"GigabitEthernet1/0/1"}, Type: "TRUNK", Protocol: protocol})
					}
					rows = channels
				} else {
					if mode == "last-incomplete" {
						w.WriteHeader(500)
						return
					}
					assignments := []fabricPortAssignment{}
					if mode != "missing" {
						kind := "USER_DEVICE"
						if mode == "assignment-conflict" {
							kind = "ACCESS_POINT"
						}
						assignments = append(assignments, fabricPortAssignment{ID: "assignment", FabricID: "fabric", DeviceID: "edge-id", Interface: "GigabitEthernet1/0/2", Type: kind})
					}
					rows = assignments
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"response": rows}); err != nil {
					t.Error(err)
				}
			})
			ctx := context.Background()
			raw := string(portChannelTestModel(`[{"id":7,"connected_device_type":"TRUNK","protocol":"LACP","interface_names":["GigabitEthernet1/0/1"]}]`))
			raw = strings.Replace(raw, `"port_channels":`, `"port_assignments":[{"interface_name":"GigabitEthernet1/0/2","connected_device_type":"USER_DEVICE"}],"port_channels":`, 1)
			config.Spec.Source.Inline.Raw = []byte(raw)
			config.Spec.Mode = configapi.NetworkControllerApplyModeReport
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			for _, member := range members {
				for _, obj := range []ctrlclient.Object{member.device, member.node} {
					if err := a.statusWriter.Delete(ctx, obj); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			if mode == "last-incomplete" {
				if len(config.Status.Drift) != 0 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("partial report published")
				}
				return
			}
			expected := 1
			if mode == "matching" {
				expected = 0
			}
			if mode == "missing" {
				expected = 2
			}
			if len(config.Status.Drift) != expected || len(config.Status.SectionStatus) != 1 || config.Status.SectionStatus[0].ResourceCount != 2 {
				t.Fatal("incomplete combined report", config.Status)
			}
			for _, entry := range config.Status.Drift {
				change := configapi.NetworkControllerDriftChangeUpdate
				if mode == "missing" {
					change = configapi.NetworkControllerDriftChangeAdd
				}
				if entry.Change != change {
					t.Fatal("incorrect combined drift")
				}
			}
		})
	}
}
