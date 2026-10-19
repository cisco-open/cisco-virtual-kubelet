// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

func TestFabricAuthenticationAcknowledgedRecoveryAndAtomicReceipt(t *testing.T) {
	for _, mode := range []string{"accepted", "unknown", "running", "failed-child", "foreign-task", "voice-changed", "membership-changed", "source", "owner", "missing-member", "denied", "publish-denied", "publish-incomplete", "publish-lost"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, f, devices := fabricAuthManagedFixture(t)
			f.afterWrite = func() {
				now := time.Now().UnixMilli()
				f.taskRows = []map[string]any{{"id": "fabric-auth-task", "startTime": now, "endTime": now, "isError": false}}
			}
			if _, err := a.prepareFabricAuthentication(ctx, config, source, "Global/Lab"); err != nil {
				t.Fatal(err)
			}
			permit, _ := configurationGroupDispatchFixture(t, a, devices, true)
			if _, err := a.dispatchFabricAuthentication(ctx, config, source, "Global/Lab", permit); err != nil {
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
			case "running":
				f.taskRows[0]["endTime"] = 0
			case "failed-child":
				now := time.Now().UnixMilli()
				f.taskRows = append(f.taskRows, map[string]any{"id": "failed-child", "parentId": "fabric-auth-task", "rootId": "fabric-auth-task", "startTime": now, "endTime": now, "isError": true})
			case "foreign-task":
				f.taskRows[0]["id"] = "foreign-task"
			case "voice-changed":
				v := false
				f.profile.Voice = &v
			case "membership-changed":
				f.memberships["zone-id"] = []fabricDeviceMembership{}
			case "source":
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case "owner":
				nc.Status.Configuration.Owners = nil
			}
			if mode == "unknown" || mode == "owner" {
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
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
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error {
				if mode == "denied" {
					return errors.New("fixture recovery denied")
				}
				return nil
			}
			restarted := *a
			err = restarted.beginFabricAuthenticationVerification(ctx, config, source, "Global/Lab", members, authorize, time.Now())
			success := mode == "accepted" || mode == "unknown" || strings.HasPrefix(mode, "publish-")
			if (err == nil) != success {
				t.Fatal("recovery outcome", mode, err)
			}
			if success {
				expected = "Verifying"
			}
			state := journalState(t, a)
			if state.Operation.Phase != expected || len(state.ActionApplications) != 0 || f.writes != 1 {
				t.Fatal("recovery replayed or released work")
			}
			if !success {
				return
			}
			before := state.Operation.DeepCopy()
			receiptMembers := members
			if mode == "publish-incomplete" {
				receiptMembers = nil
			}
			if mode == "publish-lost" {
				restarted.statusWriter = lostJournalAckClient{restarted.statusWriter}
			}
			err = restarted.recordFabricAuthenticationApplication(ctx, config, before.ID, "Global/Lab", receiptMembers, func(context.Context, []*ops.ControllerConfigurationReservation) error {
				if mode == "publish-denied" {
					return errors.New("fixture publication denied")
				}
				return nil
			}, time.Now())
			state = journalState(t, a)
			published := mode == "accepted" || mode == "unknown" || mode == "publish-lost"
			if (err == nil) != (published && mode != "publish-lost") {
				t.Fatal("publication outcome", err)
			}
			if published {
				if state.Operation.Phase != "Verified" || len(state.ActionApplications) != 1 || !strings.HasPrefix(state.ActionApplications[0].Resource, "fabric-auth:receipt:v2:") {
					t.Fatal("verified journal and receipt were not atomic")
				}
			} else if !reflect.DeepEqual(before, state.Operation) || len(state.ActionApplications) != 0 {
				t.Fatal("denied receipt publication changed evidence")
			}
			if err := a.statusReader.List(ctx, &list); err != nil {
				t.Fatal(err)
			}
			for _, member := range list.Items {
				if member.Status.Completion != nil || member.Status.ManagerSettledAt != nil || member.Status.Grant.Phase != "Held" {
					t.Fatal("receipt publication released device reservations")
				}
			}
			if f.writes != 1 {
				t.Fatal("receipt publication replayed native update")
			}
		})
	}
}
