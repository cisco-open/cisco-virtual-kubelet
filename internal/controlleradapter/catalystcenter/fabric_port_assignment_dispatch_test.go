// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestFabricPortAssignmentDispatch(t *testing.T) {
	for _, mode := range []string{"accepted", "denied", "lost", "revoked", "missing-permit"} {
		t.Run(mode, func(t *testing.T) {
			posts := 0
			created := false
			var afterPost func()
			a, config, _, members, _ := portChannelJournalFixture(t, "assignment/"+("valid"), func(w http.ResponseWriter, r *http.Request) {
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
					t.Error("unexpected method")
					w.WriteHeader(400)
					return
				}
				posts++
				created = true
				var body []map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body[0]["id"] != nil || body[0]["networkDeviceId"] != "edge-id" || body[0]["interfaceName"] != "GigabitEthernet1/0/1" {
					t.Error("wrong native payload")
				}
				if afterPost != nil {
					afterPost()
				}
				if mode == "lost" {
					w.WriteHeader(500)
					return
				}
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
			permit, revoke := configurationGroupDispatchFixture(t, a, members, true)
			if mode == "denied" {
				revoke()
			}
			if mode == "revoked" {
				afterPost = revoke
			}
			if mode == "missing-permit" {
				permit = nil
			}
			task, err := a.dispatchFabricPortAssignment(ctx, config, source, "edge", "GigabitEthernet1/0/1", permit)
			if (err == nil) != (mode == "accepted") {
				t.Fatal("dispatch outcome", err)
			}
			state := journalState(t, a).Operation
			switch mode {
			case "accepted":
				if state.Phase != "Accepted" || state.TaskID != "task" || task.ID != "task" {
					t.Fatal("accepted task not retained")
				}
			case "denied", "missing-permit":
				if state.Phase != "Prepared" || state.SubmittedAt != nil || posts != 0 {
					t.Fatal("unauthorized dispatch advanced")
				}
			default:
				if state.Phase != "Submitting" || state.TaskID != "" {
					t.Fatal("uncertain submission was cleared")
				}
			}
			if mode == "revoked" && task.ID != "task" {
				t.Fatal("known task lost on receipt refusal")
			}
			expected := 1
			if mode == "denied" || mode == "missing-permit" {
				expected = 0
			}
			if posts != expected {
				t.Fatal("dispatch count", posts)
			}
			restarted := *a
			if _, err := restarted.dispatchFabricPortAssignment(ctx, config, source, "edge", "GigabitEthernet1/0/1", permit); err == nil || posts != expected {
				t.Fatal("restart replayed dispatch")
			}
		})
	}
}
