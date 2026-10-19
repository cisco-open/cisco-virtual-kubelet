// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	"net/http"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

func TestExtranetPublicRepeatedUpdates(t *testing.T) {
	for _, mode := range []string{"complete", "retained-predecessor", "late-predecessor"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			posts := 0
			var native *fabricExtranetPolicy
			tasks := map[string]int64{}
			a, config, _, _, _ := extranetManagedFixture(t, "valid", func(w http.ResponseWriter, r *http.Request) bool {
				reply := func(value any) { json.NewEncoder(w).Encode(map[string]any{"response": value}) }
				if r.URL.Path == fabricL3NetworksPath {
					reply([]fabricL3Network{{ID: "services", Name: "Services", Fabrics: []string{"fabric"}}, {ID: "corp", Name: "Corp", Fabrics: []string{"fabric"}}, {ID: "guest", Name: "Guest", Fabrics: []string{"fabric"}}, {ID: "iot", Name: "IoT", Fabrics: []string{"fabric"}}})
					return true
				}
				if r.URL.Path == fabricExtranetPath {
					if r.Method == "GET" {
						rows := []fabricExtranetPolicy{}
						if native != nil {
							rows = append(rows, *native)
						}
						reply(rows)
						return true
					}
					expected := "POST"
					if native != nil {
						expected = "PUT"
					}
					var rows []fabricExtranetPolicy
					if r.Method != expected || json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 {
						t.Error("wrong extranet mutation")
						w.WriteHeader(400)
						return true
					}
					if native != nil && rows[0].ID != native.ID {
						t.Error("changed native UUID")
					}
					native = &rows[0]
					native.ID = "policy"
					posts++
					id := fmt.Sprintf("task-%d", posts)
					tasks[id] = time.Now().UnixMilli()
					reply(map[string]string{"taskId": id})
					return true
				}
				if strings.HasPrefix(r.URL.Path, taskPath) {
					id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
					stamp, exists := tasks[id]
					if !exists {
						w.WriteHeader(404)
						return true
					}
					row := map[string]any{"id": id, "isError": false, "startTime": stamp - 1000, "endTime": stamp}
					if strings.HasSuffix(r.URL.Path, "/tree") {
						reply([]map[string]any{row})
					} else {
						reply(row)
					}
					return true
				}
				return false
			})
			reconcile := func() {
				t.Helper()
				if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
					t.Fatal(err)
				}
			}
			retire := func(rows []ops.ControllerConfigurationReservation) {
				t.Helper()
				for i := range rows {
					var fresh ops.ControllerConfigurationReservation
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(&rows[i]), &fresh); err != nil {
						t.Fatal(err)
					}
					if err := contract.RetireSettledGroupReservation(ctx, a.statusReader, a.statusWriter, &fresh, func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }, time.Now()); err != nil {
						t.Fatal("retire predecessor", err)
					}
				}
			}
			var originalCreation *cvk.NetworkControllerConfigurationActionApplication
			var previousUpdate *cvk.NetworkControllerConfigurationActionApplication
			var previousRows []ops.ControllerConfigurationReservation
			for cycle := 0; cycle < 4; cycle++ {
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
					t.Fatal(err)
				}
				subscribers := []string{"Corp"}
				if cycle >= 1 {
					subscribers = append(subscribers, "Guest")
				}
				if cycle >= 2 {
					subscribers = append(subscribers, "IoT")
				}
				if cycle == 3 {
					native.Subscribers = []string{"Corp", "Guest"}
				} // Repair an additive drift; no membership is removed by CVK.
				config.Spec.Source.Inline.Raw, _ = json.Marshal(map[string]any{"catalyst_center": map[string]any{"fabric": map[string]any{"extranet_policies": []any{map[string]any{"name": "Shared", "provider_virtual_network": "Services", "subscriber_virtual_networks": subscribers, "fabric_sites": []string{"Global/Lab"}}}}}})
				config.Generation = int64(cycle + 1)
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				a.configurationAdmission = nil
				a.workerPodName, a.workerPodUID = "connector", "connector-uid"
				if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
					t.Fatal(err)
				}
				reconcile()
				op := journalState(t, a).Operation
				if op == nil || op.Phase != "Prepared" {
					t.Fatal("no public preparation", cycle, op)
				}
				var physical []configurationDispatchFixtureMember
				for _, target := range op.PhysicalGroup.Targets {
					var d cvk.CiscoDevice
					var n core.Node
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: config.Namespace, Name: target.Device.Name}, &d); err != nil {
						t.Fatal(err)
					}
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: target.Node.Name}, &n); err != nil {
						t.Fatal(err)
					}
					physical = append(physical, configurationDispatchFixtureMember{&d, &n})
				}
				configurationGroupDispatchFixture(t, a, physical, false, cycle > 0)
				a.configurationAdmission = nil
				installFabricGroupAuthorityFixture(t, a, cycle > 0)
				reconcile()
				if current := journalState(t, a).Operation; current.Phase != "Accepted" {
					t.Fatal("not accepted", cycle, current.Phase)
				}
				reconcile()
				if current := journalState(t, a).Operation; current.Phase != "Verifying" {
					t.Fatal("not verifying", cycle, current.Phase)
				}
				if cycle == 2 && mode == "late-predecessor" {
					callbacks := 0
					a.configurationAdmission.authorize = func(context.Context, *ops.ControllerConfigurationReservation) error {
						callbacks++
						if callbacks == len(physical)+1 {
							late := previousRows[0].DeepCopy()
							late.UID = "late-prior-evidence"
							late.ResourceVersion = ""
							late.DeletionTimestamp = nil
							if err := a.statusWriter.Create(ctx, late); err != nil {
								t.Fatal(err)
							}
						}
						return nil
					}
				}
				reconcile()
				if cycle == 2 && mode != "complete" {
					for range 2 {
						if current := journalState(t, a).Operation; current.Phase != "Verifying" {
							t.Fatal("predecessor evidence did not fence publication", mode)
						}
						if !reflect.DeepEqual(previousUpdate, journalState(t, a).ActionApplications[1].DeepCopy()) {
							t.Fatal("predecessor overwritten")
						}
						reconcile()
					}
					if posts != cycle+1 {
						t.Fatal("blocked publication replayed mutation")
					}
					if mode == "late-predecessor" {
						return
					} // Foreign late evidence requires investigation, not automatic deletion.
					retire(previousRows)
					previousRows = nil
					reconcile()
				}
				state := journalState(t, a)
				count := 1
				if cycle > 0 {
					count = 2
				}
				if state.Operation.Phase != "Verified" || len(state.ActionApplications) != count {
					t.Fatal("receipt history grew or did not verify", cycle, state.Operation.Phase, len(state.ActionApplications))
				}
				if cycle == 0 {
					originalCreation = state.ActionApplications[0].DeepCopy()
				} else {
					if !reflect.DeepEqual(originalCreation, state.ActionApplications[0].DeepCopy()) {
						t.Fatal("creation ownership replaced")
					}
					update := state.ActionApplications[1].DeepCopy()
					if update.Resource != "extranet-expansion:policy" || update.Operation.ID != op.ID {
						t.Fatal("unstable or old update receipt", update.Resource)
					}
					if previousUpdate != nil && update.Operation.ID == previousUpdate.Operation.ID {
						t.Fatal("update receipt did not advance")
					}
					previousUpdate = update
				}
				a.configurationAdmission = nil
				testFabricHandoffCompletion(t, a, config, true)
				var rows ops.ControllerConfigurationReservationList
				if err := a.statusReader.List(ctx, &rows); err != nil {
					t.Fatal(err)
				}
				currentRows := []ops.ControllerConfigurationReservation{}
				for _, row := range rows.Items {
					if row.Spec.OperationID == op.ID {
						currentRows = append(currentRows, row)
					}
				}
				if cycle == 1 {
					previousRows = append([]ops.ControllerConfigurationReservation(nil), currentRows...)
				}
				if !(mode == "retained-predecessor" && cycle == 1) {
					retire(currentRows)
				}
				// Device actors, not the adapter, release actual lease holders. Preserve
				// lease objects/UIDs so the next grant must advance their incarnation.
				var leases coordination.LeaseList
				if err := a.statusReader.List(ctx, &leases); err != nil {
					t.Fatal(err)
				}
				for i := range leases.Items {
					empty := ""
					leases.Items[i].Spec.HolderIdentity = &empty
					if err := a.statusWriter.Update(ctx, &leases.Items[i]); err != nil {
						t.Fatal(err)
					}
				}
				if posts != cycle+1 {
					t.Fatal("cycle replayed native mutation")
				}
			}
			a.configurationAdmission = nil
			if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
				t.Fatal("matching policy requested mutation authority")
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				reconcile()
				var fresh configapi.NetworkControllerConfig
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &fresh); err != nil {
					t.Fatal(err)
				}
				if fresh.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || journalState(t, a).Operation != nil || len(journalState(t, a).ActionApplications) != 2 || posts != 4 {
					t.Fatal("reapplication did not converge")
				}
			}
		})
	}
}
