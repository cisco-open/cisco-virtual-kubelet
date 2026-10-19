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

func TestMulticastVNDispatch(t *testing.T) {
	for _, action := range []string{"create", "update"} {
		for _, mode := range []string{"accepted", "denied", "missing-permit", "lost", "revoked"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				posts := 0
				var afterPost func()
				a, config, source, members := multicastVNJournalFixture(t, action, func(w http.ResponseWriter, r *http.Request) bool {
					if r.Method == "GET" {
						return false
					}
					expected := "POST"
					if action == "update" {
						expected = "PUT"
					}
					if r.Method != expected || r.URL.Path != fabricMulticastVNsPath {
						t.Error("unexpected mutation", r.Method, r.URL.Path)
						w.WriteHeader(400)
						return true
					}
					posts++
					var body []map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body[0]["virtualNetworkName"] != "Corp" || body[0]["fabricId"] != "fabric" {
						t.Error("invalid multicast payload")
						w.WriteHeader(400)
						return true
					}
					if action == "create" && body[0]["id"] != nil || action == "update" && body[0]["id"] != "multicast-Corp" {
						t.Error("logical VN anchor used as native multicast identity")
					}
					if afterPost != nil {
						afterPost()
					}
					if mode == "lost" {
						w.WriteHeader(500)
						return true
					}
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					return true
				})
				ctx := context.Background()
				if _, err := a.prepareFabricMulticastVN(ctx, config, source, "Global/Lab", "Corp"); err != nil {
					t.Fatal("prepare", err)
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
				task, err := a.dispatchFabricMulticastVN(ctx, config, source, "Global/Lab", "Corp", permit)
				if (err == nil) != (mode == "accepted") {
					t.Fatal("dispatch outcome", err)
				}
				op := journalState(t, a).Operation
				switch mode {
				case "accepted":
					if op.Phase != "Accepted" || op.TaskID != "task" || task.ID != "task" || posts != 1 {
						t.Fatal("missing accepted receipt", op, task, posts)
					}
				case "denied", "missing-permit":
					if op.Phase != "Prepared" || op.SubmittedAt != nil || posts != 0 {
						t.Fatal("unadmitted submission", op, posts)
					}
				default:
					if op.Phase != "Submitting" || op.SubmittedAt == nil || posts != 1 {
						t.Fatal("uncertainty not retained", op, posts)
					}
					if mode == "revoked" && task.ID != "task" {
						t.Fatal("known task lost")
					}
				}
				before := posts
				if _, err := a.dispatchFabricMulticastVN(ctx, config, source, "Global/Lab", "Corp", permit); err == nil || posts != before {
					t.Fatal("operation replayed", err, posts)
				}
			})
		}
	}
}
