// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

func TestFabricPortChannelRecoveryAndSettlement(t *testing.T) {
	for _, mode := range []string{"valid", "unknown", "running", "failed", "missing", "replaced", "denied", "no-task", "missing-member", "source", "owner", "publish-lost", "publish-denied", "publish-incomplete"} {
		t.Run(mode, func(t *testing.T) {
			posts := 0
			var revise *bool
			a, config, source, physical, revised := portChannelJournalFixture(t, "recovery-"+mode, func(w http.ResponseWriter, r *http.Request) {
				posts++
				*revise = true
				fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
			})
			revise = revised
			ctx := context.Background()
			if _, err := a.prepareFabricPortChannel(ctx, config, source, "edge", 7); err != nil {
				t.Fatal(err)
			}
			permit, _ := configurationGroupDispatchFixture(t, a, physical, true)
			if _, err := a.dispatchFabricPortChannel(ctx, config, source, "edge", 7, permit); err != nil {
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
			err = restarted.beginFabricPortChannelVerification(ctx, config, source, "edge", 7, members, authorize, time.Now())
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
			err = restarted.recordFabricPortChannelApplication(ctx, config, state.Operation.ID, "edge", 7, receiptMembers, func(context.Context, []*ops.ControllerConfigurationReservation) error {
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
				if err := restarted.recordFabricPortChannelApplication(ctx, config, state.Operation.ID, "edge", 7, members, authorize, time.Now()); err == nil {
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

func TestFabricPortChannelPublicLifecycle(t *testing.T) {
	posts := 0
	var revise *bool
	a, config, _, physical, revised := portChannelJournalFixture(t, "recovery-public", func(w http.ResponseWriter, r *http.Request) {
		posts++
		*revise = true
		fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
	})
	revise = revised
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
