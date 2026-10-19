// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestFabricPendingEventOwnedRecovery(t *testing.T) {
	for _, mode := range []string{"accepted", "unknown", "running", "event-pending", "foreign-owner", "late-owner", "late-source", "late-node", "failed-task", "changed-group"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Second)
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"fabric"}
			config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"fabric":{"fabric_sites":[{"name":"Global/Lab/Building","pub_sub_enabled":true,"authentication_template":{"name":"Closed Authentication"},"reconfigure":true}]}}}`)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: config.Namespace, UID: "device-uid"}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, PhysicalIdentity: "serial-a", Address: "198.51.100.101"}}
			device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: "device-uid", NodeName: "edge", NodeUID: "node-uid", PhysicalIdentity: "serial-a"}
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "edge", UID: "node-uid"}}
			node.Status.NodeInfo.MachineID = "serial-a"
			for _, object := range []ctrlclient.Object{device, node} {
				if err := a.statusWriter.Create(ctx, object); err != nil {
					t.Fatal(err)
				}
			}
			want := fabricSiteIntent{path: "Global/Lab/Building", authentication: "Closed Authentication", pubsub: true}
			event := fabricPendingEvent{ID: "event-id", FabricID: "fabric-id"}
			targets := []contract.GroupTargetIdentity{{InventoryID: "inventory-id", PhysicalIdentity: "serial-a", Device: ops.ConfigurationReservationReference{Name: "edge", UID: "device-uid"}, Node: ops.ConfigurationReservationReference{Name: "edge", UID: "node-uid"}}}
			op, err := a.prepareConfiguration(ctx, config, nil, "fabric", pendingFabricEventResource(want.path, "building", event), "Update", configurationResourceIdentity{target: event.ID, parent: event.FabricID, groupTargets: targets})
			if err != nil {
				t.Fatal(err)
			}
			op, err = contract.WithGroupIdentity(op, targets)
			if err != nil {
				t.Fatal(err)
			}
			op.Phase, op.TaskID = "Accepted", "task-id"
			op.SubmittedAt = &metav1.Time{Time: now}
			if mode == "unknown" {
				op.Phase = "Unknown"
			}
			if mode == "changed-group" {
				op.PhysicalGroup.Targets[0].Node.UID = "foreign"
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration.Operation = op
			if mode == "foreign-owner" {
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			// Reconstruct from persisted status as a restarted worker would.
			op = journalState(t, a).Operation.DeepCopy()
			before := op.DeepCopy()
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("recovery submitted native mutation")
					return
				}
				switch r.URL.Path {
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
				case fabricSitesPath:
					pubsub := true
					json.NewEncoder(w).Encode(map[string]any{"response": []fabricSite{{ID: "fabric-id", SiteID: "building", Authentication: "Closed Authentication", PubSub: &pubsub}}})
				case fabricDevicesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []fabricDeviceMembership{{ID: "member-id", FabricID: "fabric-id", NetworkDeviceID: "inventory-id", Roles: []string{"EDGE_NODE"}}}})
				case devicesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []Device{{ID: "inventory-id", Serial: "serial-a", ManagementIP: "198.51.100.101"}}})
				case fabricPendingEventsPath:
					rows := []fabricPendingEvent{}
					if mode == "event-pending" {
						rows = append(rows, event)
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case taskPath + "task-id/tree":
					json.NewEncoder(w).Encode(map[string]any{"response": []map[string]any{{"id": "task-id", "rootId": "task-id", "startTime": now.UnixMilli(), "endTime": now.UnixMilli(), "isError": false}}})
				case taskPath + "task-id":
					switch mode {
					case "late-owner":
						fresh, e := a.currentController(ctx)
						if e != nil {
							t.Error(e)
							return
						}
						fresh.Status.Configuration.Owners[0].ConfigUID = "foreign"
						if e := a.statusWriter.Status().Update(ctx, fresh); e != nil {
							t.Error(e)
						}
					case "late-source":
						copy := config.DeepCopy()
						copy.Generation++
						if e := a.statusWriter.Update(ctx, copy); e != nil {
							t.Error(e)
						}
					case "late-node":
						var fresh corev1.Node
						if e := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: "edge"}, &fresh); e != nil {
							t.Error(e)
							return
						}
						fresh.UID = "replacement-node"
						if e := a.statusWriter.Update(ctx, &fresh); e != nil {
							t.Error(e)
						}
					}
					end := now.UnixMilli()
					if mode == "running" {
						end = 0
					}
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "task-id", "startTime": now.UnixMilli(), "endTime": end, "isError": mode == "failed-task"}})
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			restarted := *a
			got, err := restarted.observeOwnedPendingFabricEvent(ctx, config, nil, want, op, now)
			valid := mode == "accepted" || mode == "unknown" || mode == "running" || mode == "event-pending"
			if (err == nil) != valid {
				t.Fatal("owned recovery", err)
			}
			if valid && (got.taskComplete != (mode != "running") || got.eventPending != (mode == "event-pending")) {
				t.Fatal("incorrect evidence")
			}
			if !reflect.DeepEqual(before, journalState(t, a).Operation) {
				t.Fatal("observation advanced journal")
			}
		})
	}
}
