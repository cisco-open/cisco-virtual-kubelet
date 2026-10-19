// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"reflect"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

func fabricDevicesBulkPreparationFixture(t *testing.T, mode string) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, []fabricDeviceIntent, *fabricDevicesBulkFixture) {
	t.Helper()
	ctx := context.Background()
	fixtureMode := "valid"
	if mode == "noop" || mode == "missing-peer" {
		fixtureMode = mode
	}
	c, wants, f, api := newManagedFabricDevicesBulkFixture(t, fixtureMode)
	if err := configapi.AddToScheme(api.Scheme()); err != nil {
		t.Fatal(err)
	}
	a, config := journalFixture(t)
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config.Spec.ManagedSections = []string{"fabric"}
	// Native fixture declarations contain the complete parsed inventory model.
	config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"edge0","serial_number":"FOC12345678","site":"Global/Lab/Building","state":"PROVISION","fabric_site":"Global/Lab","fabric_roles":["EDGE_NODE"]},{"name":"edge1","serial_number":"FOC12345679","site":"Global/Lab/Building","state":"PROVISION","fabric_site":"Global/Lab","fabric_roles":["EDGE_NODE"]}]}}}`)
	config.ResourceVersion = ""
	nc.ResourceVersion = ""
	if err := api.Create(ctx, config); err != nil {
		t.Fatal(err)
	}
	if err := api.Create(ctx, nc); err != nil {
		t.Fatal(err)
	}
	a.statusReader, a.statusWriter, a.client = api, api, c
	a.workerPodName, a.workerPodUID = "connector", "connector-uid"
	if mode != "missing-runtime" {
		if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	source := &configSourceSnapshot{raw: config.Spec.Source.Inline.Raw}
	return a, config, source, wants, f
}

func TestFabricDevicesBulkSourceBoundPreparation(t *testing.T) {
	for _, mode := range []string{"prepare", "noop", "changed-source", "foreign-owner", "undeclared-member", "changed-role", "missing-peer", "missing-runtime"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, wants, f := fabricDevicesBulkPreparationFixture(t, mode)
			api := a.statusWriter
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "changed-source" {
				changed := config.DeepCopy()
				changed.Generation++
				if err := api.Update(ctx, changed); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "foreign-owner" {
				nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "fabric", ConfigName: "other", ConfigUID: "other"}}}
				if err := api.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "undeclared-member" {
				wants[0].inventory.reference.Name = "other"
			}
			if mode == "changed-role" {
				wants[0].roles = []string{"CONTROL_PLANE_NODE"}
			}
			op, err := a.prepareFabricDevicesBulk(ctx, config, source, wants)
			valid := mode == "prepare" || mode == "noop"
			if (err == nil) != valid {
				t.Fatal("preparation outcome", err)
			}
			state := journalState(t, a)
			if mode == "prepare" {
				if op == nil || op.Phase != "Prepared" || op.TaskID != "" || op.SubmittedAt != nil || contract.ValidateGroupJournalIdentity(op) != nil || len(op.PhysicalGroup.Targets) != 3 || !reflect.DeepEqual(state.Operation, op) {
					t.Fatal("complete physical group not persisted")
				}
				if _, err := a.prepareFabricDevicesBulk(ctx, config, source, wants); err == nil || !reflect.DeepEqual(journalState(t, a).Operation, op) {
					t.Fatal("restart replaced retained group")
				}
			} else if op != nil || state != nil && state.Operation != nil {
				t.Fatal("refused/no-op preparation recorded a journal")
			}
			var reservations ops.ControllerConfigurationReservationList
			if err := ops.AddToScheme(api.Scheme()); err != nil {
				t.Fatal(err)
			}
			if err := api.List(ctx, &reservations); err != nil {
				t.Fatal(err)
			}
			if f.posts != 0 || len(reservations.Items) != 0 {
				t.Fatal("preparation submitted or claimed physical work")
			}
		})
	}
}
