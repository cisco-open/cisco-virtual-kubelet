// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"net/http"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

func rmaReadinessFixture(t *testing.T, config *configapi.NetworkControllerConfig, now time.Time) (*deviceReplacementSnapshot, *cvk.NetworkControllerConfigurationStatus) {
	t.Helper()
	ref, want, devices, records := rmaBindingFixture()
	records[0].CreatedAt = now.Add(-time.Minute).UnixMilli()
	snapshot, err := bindDeviceReplacement(ref, want, devices, records)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.siteID, snapshot.provisionID = "building", "provision"
	resource, err := deviceReplacementMarkResource(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	op := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: config.Name, ConfigUID: string(config.UID), Section: "inventory", Resource: resource, Action: "Create", Phase: "Verified", TargetID: "old-id", TargetSerial: "old-serial", ParentID: "building", TaskID: "mark-task", SubmittedAt: &metav1.Time{Time: now.Add(-time.Minute)}}
	state := &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: config.Name, ConfigUID: string(config.UID)}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: "inventory", Resource: "rma-mark-record:record", Operation: op, VerifiedAt: &metav1.Time{Time: now.Add(-30 * time.Second)}}}}
	return snapshot, state
}
func TestRMAReplacementReadinessBinding(t *testing.T) {
	for _, mode := range []string{"ready", "selected-spare", "marked", "unknown", "new-api-status", "failed-step", "workflow", "replaced", "foreign-serial", "missing-spare", "spare-alias", "no-receipt", "foreign-owner", "wrong-record", "old-record", "changed-site", "active"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			config := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: "config", UID: "config-uid"}}
			snapshot, state := rmaReadinessFixture(t, config, now)
			switch mode {
			case "selected-spare":
				snapshot.record.ReplacementSerial = "NEW-SERIAL"
			case "marked":
				snapshot.record.Status = "MARKED-FOR-REPLACEMENT"
			case "unknown":
				snapshot.record.Status = "SOMETHING_NEW"
			case "new-api-status":
				snapshot.record.Status = "READY_FOR_REPLACEMENT"
			case "failed-step":
				snapshot.record.WorkflowFailedStep = "IMAGE_DISTRIBUTION"
			case "workflow":
				snapshot.record.WorkflowID = "started"
			case "replaced":
				snapshot.record.ReplacedAt = now.UnixMilli()
			case "foreign-serial":
				snapshot.record.ReplacementSerial = "foreign"
			case "missing-spare":
				snapshot.replacement = nil
			case "spare-alias":
				snapshot.replacement.ID = "old-id"
			case "no-receipt":
				state.ActionApplications = nil
			case "foreign-owner":
				state.Owners[0].ConfigUID = "foreign"
			case "wrong-record":
				snapshot.record.ID = "foreign"
			case "old-record":
				snapshot.record.CreatedAt = now.Add(-time.Hour).UnixMilli()
			case "changed-site":
				snapshot.siteID = "other"
			case "active":
				state.Operation = &cvk.NetworkControllerConfigurationOperation{Phase: "Unknown"}
			}
			devices, _, err := bindOwnedReplacementReadiness(state, config, snapshot, now)
			valid := mode == "ready" || mode == "selected-spare"
			if (err == nil) != valid || (len(devices) == 2) != valid {
				t.Fatal("incorrect readiness proof", err)
			}
		})
	}
}
func TestRMAOwnedReadinessObservation(t *testing.T) {
	for _, mode := range []string{"ready", "pending-task", "failed-task", "late-source", "late-owner", "late-spare", "group", "group-missing", "group-alias", "group-node", "group-late-native", "group-late-node"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.Source.Inline.Raw = []byte(strings.Replace(rmaModel(`{"action":"REPLACE","replacement_serial_number":"NEW-SERIAL"}`), "FOC12345678", "OLD-SERIAL", 1))
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			inventory, _, actions, err := parseRMAInventory(source.raw)
			if err != nil {
				t.Fatal(err)
			}
			ref, want := inventory.devices[0].reference, actions["edge"]
			now := time.Now().UTC().Truncate(time.Second)
			snapshot, state := rmaReadinessFixture(t, config, now)
			snapshot.record.NetworkReadinessTaskID = "network-ready"
			grouped := strings.HasPrefix(mode, "group")
			if grouped {
				snapshot.target.ManagementIP = "192.0.2.10"
				snapshot.replacement.ManagementIP = "192.0.2.11"
				for _, target := range []Device{snapshot.target, *snapshot.replacement} {
					if mode == "group-missing" && target.ID == "new-id" {
						continue
					}
					serial := strings.ToLower(target.Serial)
					device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: target.ID, Namespace: config.Namespace, UID: types.UID("device-" + target.ID)}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, PhysicalIdentity: serial, Address: target.ManagementIP}}
					device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: string(device.UID), NodeName: target.ID, NodeUID: "node-" + target.ID, PhysicalIdentity: serial}
					node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: target.ID, UID: types.UID("node-" + target.ID)}}
					node.Status.NodeInfo.MachineID = serial
					if mode == "group-node" && target.ID == "new-id" {
						node.UID = "replacement-node"
					}
					if err := a.statusWriter.Create(ctx, device); err != nil {
						t.Fatal(err)
					}
					if err := a.statusWriter.Create(ctx, node); err != nil {
						t.Fatal(err)
					}
					if mode == "group-alias" && target.ID == "new-id" {
						alias := device.DeepCopy()
						alias.Namespace = "other"
						alias.ResourceVersion = ""
						alias.UID = "alias"
						if err := a.statusWriter.Create(ctx, alias); err != nil {
							t.Fatal(err)
						}
					}
				}
			}

			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration = state
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			late := false
			nativeReads := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("readiness mutated device")
					w.WriteHeader(500)
					return
				}
				var rows any
				switch r.URL.Path {
				case devicesPath:
					nativeReads++
					spare := *snapshot.replacement
					if mode == "group-late-native" && nativeReads >= 3 {
						spare.ManagementIP = "192.0.2.12"
					}
					if mode == "group-late-node" && nativeReads == 3 {
						node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: "new-id"}}
						if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: "new-id"}, node); err != nil {
							t.Error(err)
						} else {
							node.Labels = map[string]string{"changed": "true"}
							if err := a.statusWriter.Update(ctx, node); err != nil {
								t.Error(err)
							}
						}
					}
					if late && mode == "late-spare" {
						spare.ID = "changed-spare"
					}
					rows = []Device{snapshot.target, spare}
				case deviceReplacementPath:
					rows = []deviceReplacementRecord{*snapshot.record}
				case sitesPath:
					rows = sampleSites()
				case assignedDevicesPath:
					rows = []map[string]string{{"deviceId": "old-id", "siteId": "building"}}
				case wiredProvisionPath:
					rows = []wiredProvisionedDevice{{ID: "provision", SiteID: "building", DeviceID: "old-id"}}
				case taskPath + "network-ready":
					late = true
					if mode == "late-source" {
						changed := config.DeepCopy()
						changed.Spec.Source.Inline.Raw = []byte(`{}`)
						if err := a.statusWriter.Update(ctx, changed); err != nil {
							t.Error(err)
						}
					}
					if mode == "late-owner" {
						changed, err := a.currentController(ctx)
						if err != nil {
							t.Error(err)
							return
						}
						changed.Status.Configuration.Owners[0].ConfigUID = "foreign"
						if err := a.statusWriter.Status().Update(ctx, changed); err != nil {
							t.Error(err)
						}
					}
					end := now.UnixMilli()
					if mode == "pending-task" {
						end = 0
					}
					rows = map[string]any{"id": "network-ready", "startTime": now.Add(-time.Minute).UnixMilli(), "endTime": end, "isError": mode == "failed-task"}
				default:
					t.Error("unexpected readiness endpoint", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			if grouped {
				targets, err := a.observeOwnedReplacementTargets(ctx, config, source, ref, want, now)
				if (err == nil) != (mode == "group") || (len(targets) == 2) != (mode == "group") {
					t.Fatal("incorrect replacement group binding", err)
				}
				if mode == "group" && (targets[0].PhysicalIdentity != "new-serial" || targets[1].PhysicalIdentity != "old-serial" || targets[0].Device.UID != "device-new-id" || targets[1].Node.UID != "node-old-id") {
					t.Fatal("group lost canonical identities")
				}
				return
			}
			devices, err := a.observeOwnedReplacementReadiness(ctx, config, source, ref, want, now)
			if (err == nil) != (mode == "ready") || (len(devices) == 2) != (mode == "ready") {
				t.Fatal("incorrect owned readiness", err)
			}
		})
	}
}
