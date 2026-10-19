// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

func TestFabricEdgeMixedPublicLifecycle(t *testing.T) {
	for _, missing := range []string{"channel", "assignment"} {
		t.Run(missing, func(t *testing.T) {
			posts := 0
			created := false
			a, config, _, physical, _ := portChannelJournalFixture(t, "edge-mixed", func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					path := fabricPortChannelsPath
					if missing == "assignment" {
						path = fabricPortAssignmentsPath
					}
					if r.URL.Path != path {
						t.Error("recreated matching family", r.URL.Path)
					}
					var body []map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 {
						t.Error("bad native payload")
					}
					posts++
					created = true
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected method")
					w.WriteHeader(400)
					return
				}
				var rows any
				if r.URL.Path == fabricPortChannelsPath {
					channels := []fabricPortChannel{}
					if missing != "channel" || created {
						channels = append(channels, fabricPortChannel{ID: "channel", FabricID: "fabric", DeviceID: "edge-id", Name: "Port-channel99", Interfaces: []string{"GigabitEthernet1/0/1"}, Type: "TRUNK", Protocol: "LACP"})
					}
					rows = channels
				} else {
					assignments := []fabricPortAssignment{}
					if missing != "assignment" || created {
						assignments = append(assignments, fabricPortAssignment{ID: "assignment", FabricID: "fabric", DeviceID: "edge-id", Interface: "GigabitEthernet1/0/2", Type: "USER_DEVICE"})
					}
					rows = assignments
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"response": rows}); err != nil {
					t.Error(err)
				}
			})
			raw := string(portChannelTestModel(`[{"id":7,"connected_device_type":"TRUNK","protocol":"LACP","interface_names":["GigabitEthernet1/0/1"]}]`))
			raw = strings.Replace(raw, `"port_channels":`, `"port_assignments":[{"interface_name":"GigabitEthernet1/0/2","connected_device_type":"USER_DEVICE"}],"port_channels":`, 1)
			config.Spec.Source.Inline.Raw = []byte(raw)
			if err := a.statusWriter.Update(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			testPublicHandoffPreparation(t, a, config)
			configurationGroupDispatchFixture(t, a, physical, false)
			installFabricGroupAuthorityFixture(t, a)
			testPublicHandoffDispatch(t, a, config)
			a.configurationAdmission = nil
			var list ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(context.Background(), &list); err != nil {
				t.Fatal(err)
			}
			members := make([]*ops.ControllerConfigurationReservation, len(list.Items))
			for i := range list.Items {
				members[i] = list.Items[i].DeepCopy()
			}
			testPublicHandoffRecovery(t, a, config, members, "dispatch-recovery-public")
			if posts != 1 {
				t.Fatal("mixed manifest replayed or recreated matching resources", posts)
			}
		})
	}
}
