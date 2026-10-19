// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
)

func TestFabricPortAssignmentDependencies(t *testing.T) {
	for _, mode := range []string{"missing", "matching", "conflict", "channel-overlap", "not-member", "missing-port", "changed-port", "changed-peer", "changed-assignment"} {
		t.Run(mode, func(t *testing.T) {
			native := mode
			if mode == "channel-overlap" {
				native = "existing"
			}
			changed := false
			c, _, revised := portChannelDependencyFixture(t, "assignment/"+(native), func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Query().Get("fabricId") != "fabric" || r.URL.Query().Get("networkDeviceId") != "edge-id" {
					t.Error("unexpected assignment request")
					w.WriteHeader(400)
					return
				}
				rows := []fabricPortAssignment{}
				if mode == "matching" || mode == "conflict" || mode == "changed-assignment" {
					row := fabricPortAssignment{ID: "assignment", FabricID: "fabric", DeviceID: "edge-id", Interface: "GigabitEthernet1/0/2", Type: "USER_DEVICE"}
					if mode == "conflict" {
						row.Type = "ACCESS_POINT"
					}
					if mode == "changed-assignment" && changed {
						row.ID = "replacement"
					}
					rows = append(rows, row)
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"response": rows}); err != nil {
					t.Error(err)
				}
			})
			_, wants, err := parseFabricPortAssignmentDeployment(portAssignmentTestModel(`[{"interface_name":"GigabitEthernet1/0/2","connected_device_type":"USER_DEVICE"}]`))
			if err != nil {
				t.Fatal(err)
			}
			before, err := c.fabricPortAssignmentDependencies(context.Background(), wants[0])
			invalid := mode == "conflict" || mode == "channel-overlap" || mode == "not-member" || mode == "missing-port"
			if invalid {
				if err == nil || before != nil {
					t.Fatal("invalid dependency accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if before.deviceID != "edge-id" || before.fabricID != "fabric" || len(before.physical) != 2 || len(before.ports) != 1 || len(before.digest) != 64 {
				t.Fatal("incomplete snapshot")
			}
			if (len(before.selection.existing) == 1) != (mode == "matching" || mode == "changed-assignment") {
				t.Fatal("incorrect assignment classification")
			}
			*revised = true
			changed = true
			after, err := c.fabricPortAssignmentDependencies(context.Background(), wants[0])
			if err != nil {
				t.Fatal(err)
			}
			stable := mode == "missing" || mode == "matching"
			if (before.digest == after.digest) != stable {
				t.Fatal("dependency change lost")
			}
		})
	}
}

func TestFabricPortAssignmentManagedTargets(t *testing.T) {
	for _, mode := range []string{"valid", "missing-peer", "replaced-node", "late-peer"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, members, _ := portChannelJournalFixture(t, "assignment/"+(mode), func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != fabricPortAssignmentsPath {
					t.Error("unexpected request", r.Method, r.URL)
					w.WriteHeader(400)
					return
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"response": []fabricPortAssignment{}}); err != nil {
					t.Error(err)
				}
			})
			if mode == "missing-peer" {
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
			_, wants, err := parseFabricPortAssignmentDeployment(portAssignmentTestModel("[" + portAssignmentTestRow + "]"))
			if err != nil {
				t.Fatal(err)
			}
			deps, targets, err := a.client.fabricPortAssignmentTargets(ctx, a.statusReader, config.Namespace, wants[0])
			if mode != "valid" {
				if err == nil || deps != nil || targets != nil {
					t.Fatal("incomplete or changing managed scope accepted")
				}
				return
			}
			if err != nil || deps == nil || len(targets) != 2 {
				t.Fatal("missing managed footprint", err)
			}
			for _, member := range members {
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(member.node), member.node); err != nil || member.node.Spec.Unschedulable {
					t.Fatal("observation mutated scheduling", err)
				}
			}
		})
	}
}
