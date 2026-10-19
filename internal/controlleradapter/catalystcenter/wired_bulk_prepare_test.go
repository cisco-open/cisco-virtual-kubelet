// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

func TestWiredBulkPreparationPreflightsEntireSequence(t *testing.T) {
	for _, mode := range []string{"create", "update", "noop", "invalid-later-batch", "duplicate-later-batch", "foreign-owner", "changed-source", "replaced-device"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			create := mode != "update"
			a, config, source, _, f := wiredBulkJournalFixture(t, create, false)
			intent, states, err := parseWiredInventoryIntent(source.raw)
			if err != nil {
				t.Fatal(err)
			}
			batches, err := groupWiredBulkMembers(intent, states, []string{"Global", "Global/Lab", "Global/Lab/Building"}, wiredBulkGrouping{root: "Global/Lab"})
			if err != nil || len(batches) != 1 {
				t.Fatal("fixture grouping", err)
			}
			switch mode {
			case "noop":
				f.present["a"], f.present["b"] = "record-a", "record-b"
			case "invalid-later-batch":
				batches = append(batches, wiredBulkBatch{path: "Global/Lab", members: []wiredBulkMember{{desired: inventoryDeviceIntent{reference: configurationDeviceReference{Name: "missing"}, site: "Global/Lab/Building"}, state: "PROVISION"}}})
			case "duplicate-later-batch":
				batches = append(batches, batches[0])
			case "foreign-owner":
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "changed-source":
				fresh := config.DeepCopy()
				fresh.Generation++
				if err := a.statusWriter.Update(ctx, fresh); err != nil {
					t.Fatal(err)
				}
			case "replaced-device":
				f.serialB = "REPLACED"
			}
			op, err := a.prepareOwnedWiredBulkSequence(ctx, config, source, batches, time.Now())
			valid := mode == "create" || mode == "update" || mode == "noop"
			if (err == nil) != valid {
				t.Fatal("preparation outcome", err)
			}
			state := journalState(t, a)
			if mode == "create" || mode == "update" {
				if op == nil || op.Phase != "Prepared" || op.TaskID != "" || op.SubmittedAt != nil || contract.ValidateGroupJournalIdentity(op) != nil || !reflect.DeepEqual(op, state.Operation) || len(op.PhysicalGroup.Targets) != 2 {
					t.Fatal("complete group preparation was not persisted")
				}
				if (op.Action == "Create") != create {
					t.Fatal("wrong native action selected")
				}
				if _, err := a.prepareOwnedWiredBulkSequence(ctx, config, source, batches, time.Now()); err == nil || !reflect.DeepEqual(journalState(t, a).Operation, op) {
					t.Fatal("restart replaced prepared identity")
				}
			} else if op != nil || state.Operation != nil {
				t.Fatal("invalid later batch or no-op prepared a partial journal")
			}
			var reservations ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &reservations); err != nil {
				t.Fatal(err)
			}
			if f.calls != 0 || len(reservations.Items) != 0 || len(state.ActionApplications) != 0 {
				t.Fatal("preparation dispatched, claimed or completed a batch")
			}
		})
	}
}
