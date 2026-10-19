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
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

func TestExtranetAcknowledgedRecovery(t *testing.T) {
	for _, action := range []string{"create", "update"} {
		for _, mode := range []string{"public-cycle", "public-cycle-configmap", "valid", "unknown", "running", "failed", "late-child-failure", "missing", "changed-native", "denied", "no-task", "missing-member", "replaced-node", "source", "owner", "publish-task-changed", "publish-lost", "publish-denied", "publish-incomplete", "publish-changing", "public", "public-unknown", "public-denied", "public-replaced-worker"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				ctx := context.Background()
				posts := 0
				taskTime := time.Now()
				publishing := false
				publicationReads := 0
				treeReads := 0
				publicationTaskChanged := false
				var native *fabricExtranetPolicy
				nativeMode := "valid"
				if action == "update" {
					nativeMode = "expansion"
				}
				a, config, want, _, late := extranetManagedFixture(t, nativeMode, func(w http.ResponseWriter, r *http.Request) bool {
					reply := func(value any) { json.NewEncoder(w).Encode(map[string]any{"response": value}) }
					if r.Method == "POST" || r.Method == "PUT" {
						var rows []fabricExtranetPolicy
						if r.URL.Path != fabricExtranetPath || json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 {
							t.Error("invalid mutation")
							w.WriteHeader(400)
							return true
						}
						native = &rows[0]
						native.ID = "policy"
						posts++
						taskTime = time.Now()
						fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
						return true
					}
					if r.URL.Path == taskPath+"task/tree" {
						treeReads++
						end := taskTime.UnixMilli()
						if mode == "running" {
							end = 0
						}
						failed := mode == "failed" || mode == "late-child-failure" && treeReads > 1 || publicationTaskChanged
						reply([]map[string]any{{"id": "task", "isError": false, "startTime": taskTime.Add(-time.Second).UnixMilli(), "endTime": taskTime.UnixMilli()}, {"id": "child", "parentId": "task", "rootId": "task", "isError": failed, "startTime": taskTime.Add(-time.Second).UnixMilli(), "endTime": end}})
						return true
					}
					if r.URL.Path == taskPath+"task" {
						reply(map[string]any{"id": "task", "isError": false, "startTime": taskTime.Add(-time.Second).UnixMilli(), "endTime": taskTime.UnixMilli()})
						return true
					}
					if native != nil && r.URL.Path == fabricExtranetPath {
						if mode == "missing" {
							reply([]fabricExtranetPolicy{})
							return true
						}
						raw, _ := json.Marshal(native)
						var row fabricExtranetPolicy
						json.Unmarshal(raw, &row)
						if publishing && mode == "publish-changing" {
							publicationReads++
							row.ID = fmt.Sprintf("replacement-%d", publicationReads)
						}
						reply([]fabricExtranetPolicy{row})
						return true
					}
					return false
				})
				config.Spec.Source.Inline.Raw = []byte(extranetModel)
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				cycle := strings.HasPrefix(mode, "public-cycle")
				if mode == "public-cycle-configmap" {
					cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "extranet-model", Namespace: config.Namespace, UID: "extranet-model-uid"}, Data: map[string]string{"model.yaml": string(config.Spec.Source.Inline.Raw)}}
					if err := a.statusWriter.Create(ctx, cm); err != nil {
						t.Fatal(err)
					}
					config.Spec.Source.Inline = nil
					config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model.yaml"}
					if err := a.statusWriter.Update(ctx, config); err != nil {
						t.Fatal(err)
					}
				}
				source, err := loadControllerSource(ctx, a.statusReader, config)
				if err != nil {
					t.Fatal(err)
				}
				if action == "update" {
					retainExtranetCreationFixture(t, a, config, want)
				}
				var prepared *cvk.NetworkControllerConfigurationOperation
				if cycle {
					a.workerPodName, a.workerPodUID = "connector", "connector-uid"
					if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
						t.Fatal(err)
					}
					if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
						t.Fatal(err)
					}
					prepared = journalState(t, a).Operation
					if prepared == nil || prepared.Phase != "Prepared" {
						t.Fatal("public route did not prepare")
					}
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
						t.Fatal(err)
					}
					source, err = loadControllerSource(ctx, a.statusReader, config)
					if err != nil {
						t.Fatal(err)
					}
				} else {
					prepared, err = a.prepareExtranetPolicy(ctx, config, source, "Shared")
					if err != nil || prepared == nil {
						t.Fatal("prepare", err)
					}
				}
				var physical []configurationDispatchFixtureMember
				for _, target := range prepared.PhysicalGroup.Targets {
					var device cvk.CiscoDevice
					var node core.Node
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: config.Namespace, Name: target.Device.Name}, &device); err != nil {
						t.Fatal(err)
					}
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: target.Node.Name}, &node); err != nil {
						t.Fatal(err)
					}
					physical = append(physical, configurationDispatchFixtureMember{&device, &node})
				}
				permit, _ := configurationGroupDispatchFixture(t, a, physical, !cycle)
				if cycle {
					a.configurationAdmission = nil
					installFabricGroupAuthorityFixture(t, a)
					if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
						t.Fatal(err)
					}
					if current := journalState(t, a).Operation; current == nil || current.Phase != "Accepted" {
						t.Fatal("public Prepared resume did not dispatch", current)
					}
				} else if _, err := a.dispatchExtranetPolicy(ctx, config, source, "Shared", permit); err != nil {
					t.Fatal("dispatch", err)
				}
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				expected := "Accepted"
				if mode == "unknown" || mode == "public-unknown" {
					nc.Status.Configuration.Operation.Phase = "Unknown"
					expected = "Unknown"
				}
				if mode == "no-task" {
					nc.Status.Configuration.Operation.TaskID = ""
				}
				if mode == "owner" {
					nc.Status.Configuration.Owners = nil
				}
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				var rows ops.ControllerConfigurationReservationList
				if err := a.statusReader.List(ctx, &rows); err != nil {
					t.Fatal(err)
				}
				members := make([]*ops.ControllerConfigurationReservation, len(rows.Items))
				for i := range rows.Items {
					members[i] = rows.Items[i].DeepCopy()
				}
				if mode == "missing-member" {
					if err := a.statusWriter.Delete(ctx, members[0]); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "replaced-node" {
					node := physical[0].node.DeepCopy()
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(node), node); err != nil {
						t.Fatal(err)
					}
					if err := a.statusWriter.Delete(ctx, node); err != nil {
						t.Fatal(err)
					}
					node.UID = "replacement"
					node.ResourceVersion = ""
					if err := a.statusWriter.Create(ctx, node); err != nil {
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
				if mode == "changed-native" {
					late.Store(true)
				}
				authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error {
					if mode == "denied" {
						return errors.New("fixture denied")
					}
					return nil
				}
				restarted := *a
				public := strings.HasPrefix(mode, "public")
				successful := mode == "valid" || mode == "unknown" || strings.HasPrefix(mode, "publish-") || mode == "public" || mode == "public-unknown" || cycle
				if public {
					restarted.configurationAdmission = nil
					restarted.workerPodName, restarted.workerPodUID = members[0].Spec.ControllerWorkerPod.Name, members[0].Spec.ControllerWorkerPod.UID
					if err := restarted.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
						if mode == "public-denied" {
							return errors.New("denied actor")
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if mode == "public-replaced-worker" {
						restarted.workerPodUID = "replacement"
					}
					if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
						t.Fatal(err)
					}
					var observed configapi.NetworkControllerConfig
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &observed); err != nil {
						t.Fatal(err)
					}
					if observed.Status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
						t.Fatal("public recovery reported premature completion", observed.Status.Phase)
					}
				} else {
					err = restarted.beginExtranetVerification(ctx, config, source, "Shared", members, authorize, time.Now())
					if (err == nil) != successful {
						t.Fatal("verification outcome", err)
					}
				}
				if successful {
					expected = "Verifying"
				}
				state := journalState(t, a)
				initialReceipts := 0
				if action == "update" {
					initialReceipts = 1
				}
				if state.Operation.Phase != expected || len(state.ActionApplications) != initialReceipts || posts != 1 {
					t.Fatal("verification changed claims/receipts or replayed", state.Operation.Phase, posts)
				}
				if !successful {
					return
				}
				publishing = true
				originalWriter := restarted.statusWriter
				if mode == "publish-lost" {
					restarted.statusWriter = lostJournalAckClient{originalWriter}
				}
				receiptMembers := members
				if mode == "publish-incomplete" {
					receiptMembers = nil
				}
				if public {
					if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
						t.Fatal(err)
					}
				} else {
					err = restarted.recordRecoveredExtranetApplication(ctx, config, source, "Shared", receiptMembers, func(context.Context, []*ops.ControllerConfigurationReservation) error {
						if mode == "publish-task-changed" {
							publicationTaskChanged = true
						}
						if mode == "publish-denied" {
							return errors.New("denied publication")
						}
						return nil
					}, time.Now())
					published := mode != "publish-denied" && mode != "publish-incomplete" && mode != "publish-changing" && mode != "publish-task-changed"
					if (err == nil) != (published && mode != "publish-lost") {
						t.Fatal("publication outcome", err)
					}
				}
				published := mode != "publish-denied" && mode != "publish-incomplete" && mode != "publish-changing" && mode != "publish-task-changed"
				state = journalState(t, a)
				if published {
					if state.Operation.Phase != "Verified" || len(state.ActionApplications) != initialReceipts+1 {
						t.Fatal("non-atomic receipt publication", state.Operation.Phase)
					}
					nc, err = a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := contract.GroupCompletionsForReceipt(nc, members, time.Now()); err != nil {
						t.Fatal("completion cannot use receipt", err)
					}
				} else if state.Operation.Phase != "Verifying" || len(state.ActionApplications) != initialReceipts {
					t.Fatal("denied publication changed state")
				}
				var retained ops.ControllerConfigurationReservationList
				if err := a.statusReader.List(ctx, &retained); err != nil {
					t.Fatal(err)
				}
				for _, member := range retained.Items {
					if member.Status.Completion != nil || member.Status.Grant.Phase != "Held" || member.Status.ManagerSettledAt != nil {
						t.Fatal("verification released member")
					}
				}
				if posts != 1 {
					t.Fatal("recovery replayed native mutation")
				}
				restarted.statusWriter = originalWriter
				if published {
					if err := restarted.recordRecoveredExtranetApplication(ctx, config, source, "Shared", members, authorize, time.Now()); err == nil {
						t.Fatal("publication replayed")
					}
					restarted.configurationAdmission = nil
					testFabricHandoffCompletion(t, &restarted, config, cycle)
					if cycle {
						restarted.configurationAdmission = nil
						if err := restarted.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
							t.Fatal("converged intent requested permission")
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						for range 2 {
							if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
								t.Fatal(err)
							}
							var final configapi.NetworkControllerConfig
							if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &final); err != nil {
								t.Fatal(err)
							}
							if final.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || journalState(t, a).Operation != nil || len(journalState(t, a).ActionApplications) != initialReceipts+1 {
								t.Fatal("public cycle did not settle InSync", final.Status)
							}
						}
					}
					if posts != 1 {
						t.Fatal("settlement replayed native mutation")
					}
				}
			})
		}
	}
}
