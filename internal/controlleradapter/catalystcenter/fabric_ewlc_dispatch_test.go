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

func TestFabricEWLCDispatch(t *testing.T) {
	for _, mode := range []string{"accepted", "denied", "lost", "revoked", "missing-permit"} {
		t.Run(mode, func(t *testing.T) {
			posts := 0
			var afterPost func()
			a, config, source, members, _ := ewlcJournalFixture(t, "valid", func(w http.ResponseWriter, r *http.Request) {
				posts++
				var body fabricEWLCSetting
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.ID != "edge-id" || !body.Enabled || !body.Rolling.Enabled || body.Rolling.Percentage != 25 {
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
			if _, err := a.prepareFabricEWLC(ctx, config, source, "edge"); err != nil {
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
			task, err := a.dispatchFabricEWLC(ctx, config, source, "edge", permit)
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
			if _, err := restarted.dispatchFabricEWLC(ctx, config, source, "edge", permit); err == nil || posts != expected {
				t.Fatal("restart replayed dispatch")
			}
		})
	}
}
