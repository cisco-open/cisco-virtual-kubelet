// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMulticastVNRecovery(t *testing.T) {
	for _, action := range []string{"create", "update"} {
		for _, mode := range []string{"valid", "unknown", "running", "failed", "missing", "replaced", "denied", "no-task", "missing-member", "source", "owner", "publish-lost", "publish-denied", "publish-incomplete", "publish-changing"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				posts := 0
				taskTime := time.Now()
				publishing := false
				publicationReads := 0
				var native map[string]any
				a, config, source, physical := multicastVNJournalFixture(t, action, func(w http.ResponseWriter, r *http.Request) bool {
					reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
					if r.Method == "POST" || r.Method == "PUT" {
						expected := "POST"
						if action == "update" {
							expected = "PUT"
						}
						if r.URL.Path != fabricMulticastVNsPath || r.Method != expected {
							t.Error("unexpected mutation")
							w.WriteHeader(400)
							return true
						}
						var rows []map[string]any
						if json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 {
							t.Error("bad payload")
							w.WriteHeader(400)
							return true
						}
						native = rows[0]
						native["id"] = "multicast-Corp"
						rp := native["multicastRPs"].([]any)[0].(map[string]any)
						rp["ipv4Address"] = "192.0.2.1"
						posts++
						taskTime = time.Now()
						fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
						return true
					}
					if r.URL.Path == taskPath+"task/tree" {
						end := taskTime.UnixMilli()
						if mode == "running" {
							end = 0
						}
						reply([]map[string]any{{"id": "task", "isError": false, "startTime": taskTime.Add(-time.Second).UnixMilli(), "endTime": taskTime.UnixMilli()}, {"id": "child", "parentId": "task", "rootId": "task", "isError": mode == "failed", "startTime": taskTime.Add(-time.Second).UnixMilli(), "endTime": end}})
						return true
					}
					if native != nil && r.URL.Path == sitePoolsPath && mode == "replaced" {
						rows := fixtureReservations()
						rows[0].ID = "replacement"
						reply(rows)
						return true
					}
					if native != nil && r.URL.Path == fabricMulticastVNsPath {
						if mode == "missing" {
							reply([]any{})
							return true
						}
						raw, _ := json.Marshal(native)
						var row map[string]any
						json.Unmarshal(raw, &row)
						if publishing && mode == "publish-changing" {
							publicationReads++
							row["id"] = fmt.Sprintf("replaced-%d", publicationReads)
						}
						reply([]map[string]any{row})
						return true
					}
					return false
				})
				ctx := context.Background()
				if _, err := a.prepareFabricMulticastVN(ctx, config, source, "Global/Lab", "Corp"); err != nil {
					t.Fatal("prepare", err)
				}
				permit, _ := configurationGroupDispatchFixture(t, a, physical, true)
				if _, err := a.dispatchFabricMulticastVN(ctx, config, source, "Global/Lab", "Corp", permit); err != nil {
					t.Fatal("dispatch", err)
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
				var beforeRecovery ops.ControllerConfigurationReservationList
				if err := a.statusReader.List(ctx, &beforeRecovery); err != nil {
					t.Fatal(err)
				}
				authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error {
					if mode == "denied" {
						return errors.New("fixture denied")
					}
					return nil
				}
				restarted := *a
				err = restarted.beginFabricMulticastVNVerification(ctx, config, source, "Global/Lab", "Corp", members, authorize, time.Now())
				successful := mode == "valid" || mode == "unknown" || mode == "publish-lost" || mode == "publish-denied" || mode == "publish-incomplete" || mode == "publish-changing"
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
				var retained ops.ControllerConfigurationReservationList
				if err := a.statusReader.List(ctx, &retained); err != nil {
					t.Fatal(err)
				}
				if len(retained.Items) != len(beforeRecovery.Items) {
					t.Fatal("verification removed claims")
				}

				if !successful {
					return
				}
				originalWriter := restarted.statusWriter
				publishing = true
				if mode == "publish-lost" {
					restarted.statusWriter = lostJournalAckClient{originalWriter}
				}
				receiptMembers := members
				if mode == "publish-incomplete" {
					receiptMembers = nil
				}
				err = restarted.recordFabricMulticastVNApplication(ctx, config, state.Operation.ID, "Global/Lab", "Corp", receiptMembers, func(context.Context, []*ops.ControllerConfigurationReservation) error {
					if mode == "publish-denied" {
						return errors.New("fixture denied receipt")
					}
					return nil
				}, time.Now())
				published := mode != "publish-denied" && mode != "publish-incomplete" && mode != "publish-changing"
				if (err == nil) != (published && mode != "publish-lost") {
					t.Fatal("receipt outcome", err)
				}
				state = journalState(t, a)
				if published {
					if len(state.ActionApplications) != 1 || !strings.HasSuffix(state.ActionApplications[0].Resource, ":multicast-Corp") || state.ActionApplications[0].Operation.TargetID != "vn" || state.ActionApplications[0].Operation.CreatedResourceID != "" {
						t.Fatal("native UUID or logical anchor lost", state)
					}
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
					if err := restarted.recordFabricMulticastVNApplication(ctx, config, state.Operation.ID, "Global/Lab", "Corp", members, authorize, time.Now()); err == nil {
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
}
