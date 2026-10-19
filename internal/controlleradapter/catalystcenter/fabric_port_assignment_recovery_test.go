// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

func TestFabricPortAssignmentRecoveryAndSettlement(t *testing.T) {
	for _, mode := range []string{"valid", "unknown", "running", "failed", "missing", "replaced", "denied", "no-task", "missing-member", "source", "owner", "publish-lost", "publish-denied", "publish-incomplete"} {
		t.Run(mode, func(t *testing.T) {
			posts := 0
			created := false
			reads := 0
			a, config, _, physical, _ := portChannelJournalFixture(t, "assignment/"+("recovery-"+mode), func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					reads++
					rows := []fabricPortAssignment{}
					if created && mode != "missing" {
						id := "native"
						if mode == "replaced" && reads%2 == 0 {
							id = "replacement"
						}
						rows = append(rows, fabricPortAssignment{ID: id, FabricID: "fabric", DeviceID: "edge-id", Interface: "GigabitEthernet1/0/1", Type: "USER_DEVICE"})
					}
					if err := json.NewEncoder(w).Encode(map[string]any{"response": rows}); err != nil {
						t.Error(err)
					}
					return
				}
				if r.Method != "POST" {
					t.Error("unexpected method")
					w.WriteHeader(400)
					return
				}
				posts++
				created = true
				fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
			})
			ctx := context.Background()
			config.Spec.Source.Inline.Raw = portAssignmentTestModel("[" + portAssignmentTestRow + "]")
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := a.prepareFabricPortAssignment(ctx, config, source, "edge", "GigabitEthernet1/0/1"); err != nil {
				t.Fatal(err)
			}
			permit, _ := configurationGroupDispatchFixture(t, a, physical, true)
			if _, err := a.dispatchFabricPortAssignment(ctx, config, source, "edge", "GigabitEthernet1/0/1", permit); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			expected := "Accepted"
			switch mode {
			case "unknown":
				nc.Status.Configuration.Operation.Phase = "Unknown"
				expected = "Unknown"
			case "no-task":
				nc.Status.Configuration.Operation.TaskID = ""
			case "owner":
				nc.Status.Configuration.Owners = nil
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			var list ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &list); err != nil {
				t.Fatal(err)
			}
			members := make([]*ops.ControllerConfigurationReservation, len(list.Items))
			for i := range list.Items {
				members[i] = list.Items[i].DeepCopy()
			}
			if mode == "missing-member" {
				if err := a.statusWriter.Delete(ctx, members[0]); err != nil {
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
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error {
				if mode == "denied" {
					return errors.New("fixture denied")
				}
				return nil
			}
			restarted := *a
			err = restarted.beginFabricPortAssignmentVerification(ctx, config, source, "edge", "GigabitEthernet1/0/1", members, authorize, time.Now())
			successful := mode == "valid" || mode == "unknown" || mode == "publish-lost" || mode == "publish-denied" || mode == "publish-incomplete"
			if (err == nil) != successful {
				t.Fatal("verification outcome", err)
			}
			if successful {
				expected = "Verifying"
			}
			state := journalState(t, a)
			if state.Operation.Phase != expected || len(state.ActionApplications) != 0 {
				t.Fatal("verification released/cleared work")
			}
			if posts != 1 {
				t.Fatal("native replay", posts)
			}
			if !successful {
				return
			}
			originalWriter := restarted.statusWriter
			if mode == "publish-lost" {
				restarted.statusWriter = lostJournalAckClient{originalWriter}
			}
			receiptMembers := members
			if mode == "publish-incomplete" {
				receiptMembers = nil
			}
			err = restarted.recordFabricPortAssignmentApplication(ctx, config, state.Operation.ID, "edge", "GigabitEthernet1/0/1", receiptMembers, func(context.Context, []*ops.ControllerConfigurationReservation) error {
				if mode == "publish-denied" {
					return errors.New("fixture denied receipt")
				}
				return nil
			}, time.Now())
			published := mode != "publish-denied" && mode != "publish-incomplete"
			if (err == nil) != (published && mode != "publish-lost") {
				t.Fatal("receipt outcome", err)
			}
			state = journalState(t, a)
			if published {
				if state.Operation.Phase != "Verified" || len(state.ActionApplications) != 1 {
					t.Fatal("non-atomic verified receipt")
				}
				nc, err = a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := contract.GroupCompletionsForReceipt(nc, members, time.Now()); err != nil {
					t.Fatal("receipt cannot derive completion", err)
				}
			} else if state.Operation.Phase != "Verifying" || len(state.ActionApplications) != 0 {
				t.Fatal("denied publication changed state")
			}
			if err := a.statusReader.List(ctx, &list); err != nil {
				t.Fatal(err)
			}
			for _, member := range list.Items {
				if member.Status.Completion != nil || member.Status.Grant.Phase != "Held" || member.Status.ManagerSettledAt != nil {
					t.Fatal("verification released member")
				}
			}
			restarted.statusWriter = originalWriter
			if published {
				if err := restarted.recordFabricPortAssignmentApplication(ctx, config, state.Operation.ID, "edge", "GigabitEthernet1/0/1", members, authorize, time.Now()); err == nil {
					t.Fatal("receipt publication replayed")
				}
				// Reuse the existing adapter terminal fixture, including lost completion
				// acknowledgment, source deletion and partial release/manager settlement.
				testFabricHandoffCompletion(t, &restarted, config)
			}
			if posts != 1 {
				t.Fatal("settlement replayed native work")
			}
		})
	}
}

func TestFabricPortAssignmentPublicLifecycle(t *testing.T) {
	posts := 0
	created := false
	a, config, _, physical, _ := portChannelJournalFixture(t, "assignment/"+("recovery-public"), func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			rows := []fabricPortAssignment{}
			if created {
				rows = append(rows, fabricPortAssignment{ID: "native", FabricID: "fabric", DeviceID: "edge-id", Interface: "GigabitEthernet1/0/1", Type: "USER_DEVICE"})
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"response": rows}); err != nil {
				t.Error(err)
			}
			return
		}
		if r.Method != "POST" {
			t.Error("unexpected native method")
			w.WriteHeader(400)
			return
		}
		posts++
		created = true
		fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
	})
	config.Spec.Source.Inline.Raw = portAssignmentTestModel("[" + portAssignmentTestRow + "]")
	if err := a.statusWriter.Update(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	testPublicHandoffPreparation(t, a, config)
	configurationGroupDispatchFixture(t, a, physical, false)
	installFabricGroupAuthorityFixture(t, a)
	testPublicHandoffDispatch(t, a, config)
	a.configurationAdmission = nil
	var list ops.ControllerConfigurationReservationList
	if err := a.statusReader.List(context.Background(), &list); err != nil {
		t.Fatal(err)
	}
	members := make([]*ops.ControllerConfigurationReservation, len(list.Items))
	for i := range list.Items {
		members[i] = list.Items[i].DeepCopy()
	}
	testPublicHandoffRecovery(t, a, config, members, "dispatch-recovery-public")
	if posts != 1 {
		t.Fatal("public lifecycle replayed native create", posts)
	}
}

func TestFabricPortAssignmentRangePublicLifecycle(t *testing.T) {
	for index := 0; index < 2; index++ {
		t.Run(fmt.Sprint(index), func(t *testing.T) {

			posts := 0
			created := false
			target := fmt.Sprintf("GigabitEthernet1/0/%d", index+1)
			bystander := fmt.Sprintf("GigabitEthernet1/0/%d", 2-index)
			a, config, _, physical, _ := portChannelJournalFixture(t, "assignment/"+("recovery-public"), func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					rows := []fabricPortAssignment{{ID: "pre-existing", FabricID: "fabric", DeviceID: "edge-id", Interface: bystander, Type: "USER_DEVICE"}}
					if created {
						rows = append(rows, fabricPortAssignment{ID: "native", FabricID: "fabric", DeviceID: "edge-id", Interface: target, Type: "USER_DEVICE"})
					}
					if err := json.NewEncoder(w).Encode(map[string]any{"response": rows}); err != nil {
						t.Error(err)
					}
					return
				}
				if r.Method != "POST" {
					t.Error("unexpected native method")
					w.WriteHeader(400)
					return
				}
				var payload []map[string]any
				if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload) != 1 || payload[0]["interfaceName"] != target {
					t.Error("recreated existing port or selected wrong range member")
				}
				posts++
				created = true
				fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
			})
			config.Spec.Source.Inline.Raw = portAssignmentTestModel(`[{"interfaces_range":"GigabitEthernet1/0/1-1/0/2","connected_device_type":"USER_DEVICE"}]`)
			if err := a.statusWriter.Update(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			testPublicHandoffPreparation(t, a, config)
			configurationGroupDispatchFixture(t, a, physical, false)
			installFabricGroupAuthorityFixture(t, a)
			testPublicHandoffDispatch(t, a, config)
			a.configurationAdmission = nil
			var list ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(context.Background(), &list); err != nil {
				t.Fatal(err)
			}
			members := make([]*ops.ControllerConfigurationReservation, len(list.Items))
			for i := range list.Items {
				members[i] = list.Items[i].DeepCopy()
			}
			testPublicHandoffRecovery(t, a, config, members, "dispatch-recovery-public")
			if posts != 1 {
				t.Fatal("public lifecycle replayed native create", posts)
			}

		})
	}
}
