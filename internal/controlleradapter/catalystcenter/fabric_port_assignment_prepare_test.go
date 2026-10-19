// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

func TestFabricPortAssignmentPrepare(t *testing.T) {
	for _, mode := range []string{"valid", "existing", "undeclared", "lost-ack", "pruned", "changed-port"} {
		t.Run(mode, func(t *testing.T) {
			native := mode
			if mode == "existing" {
				native = "valid"
			}
			a, config, _, _, revised := portChannelJournalFixture(t, "assignment/"+(native), func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("preparation submitted mutation")
					w.WriteHeader(400)
					return
				}
				rows := []fabricPortAssignment{}
				if mode == "existing" {
					rows = append(rows, fabricPortAssignment{ID: "native", FabricID: "fabric", DeviceID: "edge-id", Interface: "GigabitEthernet1/0/2", Type: "USER_DEVICE"})
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"response": rows}); err != nil {
					t.Error(err)
				}
			})
			ctx := context.Background()
			config.Spec.Source.Inline.Raw = portAssignmentTestModel(`[{"interface_name":"GigabitEthernet1/0/2","connected_device_type":"USER_DEVICE"}]`)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "lost-ack" {
				a.statusWriter = lostJournalAckClient{a.statusWriter}
			}
			if mode == "pruned" {
				a.statusWriter = prunePreparedGroupClient{a.statusWriter}
			}
			port := "GigabitEthernet1/0/2"
			if mode == "undeclared" {
				port = "GigabitEthernet1/0/3"
			}
			op, err := a.prepareFabricPortAssignment(ctx, config, source, "edge", port)
			state := journalState(t, a)
			if mode == "existing" || mode == "undeclared" {
				if op != nil || state != nil && state.Operation != nil {
					t.Fatal("non-create intent journaled")
				}
				if (err == nil) != (mode == "existing") {
					t.Fatal("declaration outcome", err)
				}
				return
			}
			if state == nil || state.Operation == nil || state.Operation.Phase != "Prepared" {
				t.Fatal("missing durable preparation", err)
			}
			if mode == "valid" || mode == "changed-port" {
				if err != nil || op == nil || contract.ValidateGroupJournalIdentity(op) != nil || len(op.PhysicalGroup.Targets) != 2 {
					t.Fatal("incomplete group preparation", err)
				}
				if err := a.revalidateFabricPortAssignmentOperation(ctx, config, source, "edge", port, op, "Prepared"); err != nil {
					t.Fatal(err)
				}
				if mode == "changed-port" {
					*revised = true
					if err := a.revalidateFabricPortAssignmentOperation(ctx, config, source, "edge", port, op, "Prepared"); err == nil {
						t.Fatal("replaced physical port accepted")
					}
				}
				if _, err := a.prepareFabricPortAssignment(ctx, config, source, "edge", port); err == nil {
					t.Fatal("replaced active journal")
				}
			}
			var reservations ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &reservations); err != nil || len(reservations.Items) != 0 {
				t.Fatal("preparation created claims", err)
			}
		})
	}
}
