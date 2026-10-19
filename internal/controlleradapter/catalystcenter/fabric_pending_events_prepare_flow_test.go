// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestFabricPendingEventPreparationFlow(t *testing.T) {
	for _, mode := range []string{"valid", "disabled", "missing-event", "unmanaged", "late-source", "late-controller", "foreign-owner", "prepared-event-gone", "prepared-node-replaced", "prepared-owner-changed", "prepared-source-changed", "prepared-group-pruned", "prepared-phase-changed", "dispatch-valid", "dispatch-lost", "dispatch-revoked", "dispatch-guard-lost", "dispatch-recovery-public", "dispatch-recovery-valid", "dispatch-recovery-running", "dispatch-recovery-pending", "dispatch-recovery-failed", "dispatch-recovery-denied", "dispatch-recovery-unknown"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			recoveryMode := strings.HasPrefix(mode, "dispatch-recovery-")
			recovering := false
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"fabric"}
			raw := `{"catalyst_center":{"fabric":{"fabric_sites":[{"name":"Global/Lab/Building","pub_sub_enabled":true,"authentication_template":{"name":"Closed Authentication"},"reconfigure":true}]}}}`
			if mode == "disabled" {
				raw = strings.Replace(raw, `"reconfigure":true`, `"reconfigure":false`, 1)
			}
			config.Spec.Source.Inline.Raw = []byte(raw)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: config.Namespace, UID: "device-uid"}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, PhysicalIdentity: "serial-a", Address: "198.51.100.101"}}
			device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: "device-uid", NodeName: "edge", NodeUID: "node-uid", PhysicalIdentity: "serial-a"}
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "edge", UID: "node-uid"}}
			node.Status.NodeInfo.MachineID = "serial-a"
			objects := []ctrlclient.Object{node}
			if mode != "unmanaged" {
				objects = append(objects, device)
			}
			for _, object := range objects {
				if err := a.statusWriter.Create(ctx, object); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "foreign-owner" {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "fabric", ConfigName: "other", ConfigUID: "foreign"}}}
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			changed := false
			prepared := false
			posts := 0
			var afterPost func()
			guardRemoved := false
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path == fabricPendingEventsPath+"/apply" && strings.HasPrefix(mode, "dispatch-") {
					posts++
					if afterPost != nil {
						afterPost()
					}
					if mode == "dispatch-lost" {
						fmt.Fprint(w, `{`)
						return
					}
					fmt.Fprint(w, `{"response":{"taskId":"task-id"}}`)
					return
				}
				if r.Method != "GET" {
					t.Error("preparation mutated native state")
					return
				}
				if !changed {
					changed = true
					if mode == "late-source" {
						copy := config.DeepCopy()
						copy.Generation++
						if err := a.statusWriter.Update(ctx, copy); err != nil {
							t.Error(err)
						}
					}
					if mode == "late-controller" {
						nc, err := a.currentController(ctx)
						if err != nil {
							t.Error(err)
							return
						}
						nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{}
						if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
							t.Error(err)
						}
					}
				}
				if mode == "dispatch-guard-lost" && !guardRemoved {
					nc, err := a.currentController(ctx)
					if err != nil {
						t.Error(err)
						return
					}
					if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Submitting" {
						var fresh corev1.Node
						if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(node), &fresh); err != nil {
							t.Error(err)
							return
						}
						fresh.Spec.Taints = nil
						if err := a.statusWriter.Update(ctx, &fresh); err != nil {
							t.Error(err)
							return
						}
						guardRemoved = true
					}
				}
				switch r.URL.Path {
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
				case fabricSitesPath:
					pubsub := true
					json.NewEncoder(w).Encode(map[string]any{"response": []fabricSite{{ID: "fabric-id", SiteID: "building", Authentication: "Closed Authentication", PubSub: &pubsub}}})
				case fabricDevicesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []fabricDeviceMembership{{ID: "member-id", NetworkDeviceID: "inventory-id", FabricID: "fabric-id", Roles: []string{"EDGE_NODE"}}}})
				case devicesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []Device{{ID: "inventory-id", Serial: "serial-a", ManagementIP: "198.51.100.101"}}})
				case fabricPendingEventsPath:
					rows := []fabricPendingEvent{{ID: "event-id", FabricID: "fabric-id"}}
					if mode == "missing-event" || prepared && mode == "prepared-event-gone" || recovering && mode != "dispatch-recovery-pending" {
						rows = []fabricPendingEvent{}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case taskPath + "task-id/tree":
					current, e := a.currentController(ctx)
					if e != nil {
						t.Error(e)
						return
					}
					at := current.Status.Configuration.Operation.SubmittedAt.UnixMilli()
					json.NewEncoder(w).Encode(map[string]any{"response": []map[string]any{{"id": "task-id", "rootId": "task-id", "startTime": at, "endTime": at, "isError": false}}})
				case taskPath + "task-id":
					current, e := a.currentController(ctx)
					if e != nil {
						t.Error(e)
						return
					}
					at := current.Status.Configuration.Operation.SubmittedAt.UnixMilli()
					end := at
					if mode == "dispatch-recovery-running" {
						end = 0
					}
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "task-id", "startTime": at, "endTime": end, "isError": mode == "dispatch-recovery-failed"}})
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			want := fabricSiteIntent{path: "Global/Lab/Building", authentication: "Closed Authentication", pubsub: true}
			op, err := a.preparePendingFabricEvent(ctx, config, nil, want, "event-id")
			valid := mode == "valid" || strings.HasPrefix(mode, "prepared-") || strings.HasPrefix(mode, "dispatch-")
			if (err == nil) != valid {
				t.Fatal("preparation result", err)
			}
			state := journalState(t, a)
			if !valid {
				if state != nil && state.Operation != nil {
					t.Fatal("invalid preparation retained operation")
				}
				return
			}
			if op.Phase != "Prepared" || op.TargetID != "event-id" || op.ParentID != "fabric-id" || contract.ValidateGroupJournalIdentity(op) != nil {
				t.Fatal("missing bound group")
			}
			if strings.HasPrefix(mode, "dispatch-") {
				permit, revoke := pendingEventDispatchPermitFixture(t, a, device, node)
				if mode == "dispatch-revoked" {
					afterPost = revoke
				}
				task, err := a.dispatchPendingFabricEvent(ctx, config, nil, want, permit)
				if (err == nil) != (mode == "dispatch-valid" || recoveryMode) {
					t.Fatal("dispatch result", err)
				}
				stored := journalState(t, a).Operation
				expected := "Submitting"
				if mode == "dispatch-valid" || recoveryMode {
					expected = "Accepted"
				}
				expectedPosts := 1
				if mode == "dispatch-guard-lost" {
					expectedPosts = 0
				}
				if stored.Phase != expected || posts != expectedPosts {
					t.Fatal("unexpected dispatch state", stored.Phase, posts)
				}
				if (mode == "dispatch-valid" || mode == "dispatch-revoked" || recoveryMode) && task.ID != "task-id" {
					t.Fatal("lost known task")
				}
				if (mode == "dispatch-valid" || recoveryMode) && stored.TaskID != task.ID {
					t.Fatal("lost durable task")
				}
				if _, err := a.dispatchPendingFabricEvent(ctx, config, nil, want, permit); err == nil || posts != expectedPosts {
					t.Fatal("dispatch replayed")
				}
				if recoveryMode {
					recovering = true
					if mode == "dispatch-recovery-unknown" {
						nc, e := a.currentController(ctx)
						if e != nil {
							t.Fatal(e)
						}
						nc.Status.Configuration.Operation.Phase = "Unknown"
						if e := a.statusWriter.Status().Update(ctx, nc); e != nil {
							t.Fatal(e)
						}
					}
					var list ops.ControllerConfigurationReservationList
					if e := a.statusReader.List(ctx, &list); e != nil {
						t.Fatal(e)
					}
					members := make([]*ops.ControllerConfigurationReservation, len(list.Items))
					for i := range list.Items {
						members[i] = list.Items[i].DeepCopy()
					}
					restarted := *a
					var err error
					if mode == "dispatch-recovery-public" {
						restarted.workerPodName, restarted.workerPodUID = members[0].Spec.ControllerWorkerPod.Name, members[0].Spec.ControllerWorkerPod.UID
						if e := restarted.ConfigureConfigurationReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); e != nil {
							t.Fatal(e)
						}
						for step := 0; step < 2; step++ {
							result, e := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)})
							if e != nil || result.RequeueAfter != 5*time.Second {
								t.Fatalf("public event recovery result=%+v error=%v", result, e)
							}
							var fresh configapi.NetworkControllerConfig
							if e := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &fresh); e != nil {
								t.Fatal(e)
							}
							if fresh.Status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
								t.Fatalf("event status=%s", fresh.Status.Phase)
							}
							condition := meta.FindStatusCondition(fresh.Status.Conditions, configapi.NetworkControllerConfigConditionReady)
							expectedReason := "ApplyProgressing"
							if step == 1 {
								expectedReason = "GroupConvergenceVerificationUnavailable"
							}
							if condition == nil || condition.Reason != expectedReason || condition.Status != metav1.ConditionFalse {
								t.Fatalf("recovery readiness=%+v", condition)
							}
							if step == 1 && (len(fresh.Status.SectionStatus) != 1 || !strings.Contains(fresh.Status.SectionStatus[0].Message, "not implemented")) {
								t.Fatal("missing actionable convergence limitation")
							}
							if journalState(t, a).Operation.Phase != "Verifying" {
								t.Fatal("event acknowledgement did not enter verification")
							}
						}
					} else {
						err = restarted.beginPendingFabricEventVerification(ctx, config, nil, want, members, func(context.Context, []*ops.ControllerConfigurationReservation) error {
							if mode == "dispatch-recovery-denied" {
								return errors.New("writer authorization denied")
							}
							return nil
						}, time.Now())
					}
					verified := mode == "dispatch-recovery-public" || mode == "dispatch-recovery-valid" || mode == "dispatch-recovery-unknown"
					if (err == nil) != verified {
						t.Fatal("composed verification", err)
					}
					state := journalState(t, a)
					if (state.Operation.Phase == "Verifying") != verified || len(state.ActionApplications) != 0 || posts != 1 {
						t.Fatal("premature verification/release or replay")
					}
					if err := a.statusReader.List(ctx, &list); err != nil {
						t.Fatal(err)
					}
					for _, member := range list.Items {
						if member.Status.Claim == nil || member.Status.Grant == nil || member.Status.Grant.Phase != "Held" || member.Status.Completion != nil || member.Status.ManagerSettledAt != nil {
							t.Fatal("lost recovery fence")
						}
					}
				}
				return
			}
			prepared = true
			switch mode {
			case "prepared-node-replaced":
				var fresh corev1.Node
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: "edge"}, &fresh); err != nil {
					t.Fatal(err)
				}
				fresh.UID = "replacement"
				if err := a.statusWriter.Update(ctx, &fresh); err != nil {
					t.Fatal(err)
				}
			case "prepared-owner-changed":
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "prepared-source-changed":
				copy := config.DeepCopy()
				copy.Generation++
				if err := a.statusWriter.Update(ctx, copy); err != nil {
					t.Fatal(err)
				}
			case "prepared-group-pruned":
				op.PhysicalGroup = nil
			case "prepared-phase-changed":
				op.Phase = "Submitting"
			}
			if err := a.revalidatePreparedPendingFabricEvent(ctx, config, nil, want, op); (err == nil) != (mode == "valid") {
				t.Fatal("prepared revalidation", err)
			}
			if _, err := a.preparePendingFabricEvent(ctx, config, nil, want, "event-id"); err == nil {
				t.Fatal("duplicate prepared operation")
			}
		})
	}
}
