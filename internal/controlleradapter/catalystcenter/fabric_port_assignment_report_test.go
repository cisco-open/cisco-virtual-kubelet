// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestFabricPortAssignmentReport(t *testing.T) {
	for _, mode := range []string{"matching", "missing", "conflict", "not-member", "changing"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			a, config, _, managed, _ := portChannelJournalFixture(t, "assignment/"+(mode), func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("report mutation")
					w.WriteHeader(400)
					return
				}
				reads++
				rows := []fabricPortAssignment{}
				if mode != "missing" {
					row := fabricPortAssignment{ID: "native", FabricID: "fabric", DeviceID: "edge-id", Interface: "GigabitEthernet1/0/1", Type: "USER_DEVICE"}
					if mode == "conflict" {
						row.Type = "ACCESS_POINT"
					}
					if mode == "changing" && reads%2 == 0 {
						row.ID = "replacement"
					}
					rows = append(rows, row)
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"response": rows}); err != nil {
					t.Error(err)
				}
			})
			config.Spec.Source.Inline.Raw = portAssignmentTestModel("[" + portAssignmentTestRow + "]")
			ctx := context.Background()
			// Report must work without managed-device objects, Nodes or reservation
			// configuration. Native dependencies are observed without physical authority.
			for _, member := range managed {
				for _, obj := range []ctrlclient.Object{member.device, member.node} {
					if err := a.statusWriter.Delete(ctx, obj); err != nil {
						t.Fatal(err)
					}
				}
			}
			config.Spec.Mode = configapi.NetworkControllerApplyModeReport
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			retained := journalState(t, a)
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			invalid := mode == "not-member" || mode == "changing"
			condition := meta.FindStatusCondition(config.Status.Conditions, configapi.NetworkControllerConfigConditionIntentValid)
			if condition == nil {
				t.Fatal("missing report condition")
			}
			if invalid {
				if condition.Reason != "ObservationFailed" || len(config.Status.Drift) != 0 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("incomplete evidence published as drift/compliance", config.Status)
				}
			} else {
				expected := 0
				if mode == "missing" {
					expected = 1
				}
				if mode == "conflict" {
					expected = 1
				}
				if len(config.Status.Drift) != expected {
					t.Fatal("drift count", config.Status.Drift)
				}
				if len(config.Status.SectionStatus) != 1 || config.Status.SectionStatus[0].ResourceCount != 1 {
					t.Fatal("missing channel count", config.Status.SectionStatus)
				}
				if (config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync) != (expected == 0) {
					t.Fatal("report phase", config.Status.Phase)
				}
				for _, entry := range config.Status.Drift {
					change := configapi.NetworkControllerDriftChangeAdd
					if mode == "conflict" {
						change = configapi.NetworkControllerDriftChangeUpdate
					}
					if entry.Section != "fabric" || entry.Change != change {
						t.Fatal("drift classification", entry)
					}
				}
			}
			if after := journalState(t, a); (retained == nil) != (after == nil) || after != nil && (after.Operation != nil || len(after.Owners) != 0) {
				t.Fatal("report acquired ownership")
			}
			var reservations ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &reservations); err != nil || len(reservations.Items) != 0 {
				t.Fatal("report created reservation", err)
			}
			var nodes core.NodeList
			if err := a.statusReader.List(ctx, &nodes); err != nil || len(nodes.Items) != 0 {
				t.Fatal("report unexpectedly requires nodes", err)
			}
		})
	}
}
