// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestFabricPendingEventSubmission(t *testing.T) {
	for _, mode := range []string{"accepted", "missing", "lost", "auth", "conflict", "async", "missing-task", "bad-task", "late-event", "late-fabric", "late-addition", "no-authority", "denied-authority"} {
		t.Run(mode, func(t *testing.T) {
			late := false
			posts := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				fabricID := "fabric-id"
				if late && mode == "late-fabric" {
					fabricID = "replacement-fabric"
				}
				switch r.URL.Path {
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
				case fabricSitesPath:
					pubsub := true
					json.NewEncoder(w).Encode(map[string]any{"response": []fabricSite{{ID: fabricID, SiteID: "building", Authentication: "Closed Authentication", PubSub: &pubsub}}})
				case fabricDevicesPath:
					if r.URL.Query().Get("fabricId") != fabricID {
						t.Error("lost membership scope")
					}
					json.NewEncoder(w).Encode(map[string]any{"response": []fabricDeviceMembership{{ID: "member-id", NetworkDeviceID: "device-id", FabricID: fabricID, Roles: []string{"EDGE_NODE"}}}})
				case devicesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []Device{{ID: "device-id", Serial: "FOC12345678"}}})
				case fabricPendingEventsPath:
					rows := []fabricPendingEvent{{ID: "event-id", FabricID: fabricID}}
					if mode == "missing" || late && mode == "late-event" {
						rows = []fabricPendingEvent{}
					}
					if late && mode == "late-addition" {
						rows = append(rows, fabricPendingEvent{ID: "other-event", FabricID: fabricID})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case fabricPendingEventsPath + "/apply":
					posts++
					var body []fabricPendingEvent
					if r.Method != "POST" || r.URL.RawQuery != "" || json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body[0] != (fabricPendingEvent{ID: "event-id", FabricID: "fabric-id"}) {
						t.Error("incorrect apply scope")
					}
					switch mode {
					case "lost":
						fmt.Fprint(w, `{`)
					case "auth":
						w.WriteHeader(401)
					case "conflict":
						w.WriteHeader(409)
					case "async":
						w.WriteHeader(202)
						fmt.Fprint(w, `{"response":{"taskId":"task-id"}}`)
					case "missing-task":
						fmt.Fprint(w, `{"response":{}}`)
					case "bad-task":
						fmt.Fprint(w, `{"response":{"taskId":"bad/id"}}`)
					default:
						fmt.Fprint(w, `{"response":{"taskId":"task-id","url":"https://untrusted.invalid/task"}}`)
					}
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			want := fabricSiteIntent{path: "Global/Lab/Building", authentication: "Closed Authentication", pubsub: true}
			plan, err := c.planPendingFabricEvent(context.Background(), want, "event-id")
			if mode == "missing" {
				if err == nil || posts != 0 {
					t.Fatal("missing event planned")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if plan.siteID != "building" || plan.event.ID != "event-id" || plan.resource != pendingFabricEventResource(want.path, "building", plan.event) {
				t.Fatal("lost operation binding")
			}
			late = true
			authorize := func(context.Context) error {
				if mode == "denied-authority" {
					return errors.New("authority expired")
				}
				return nil
			}
			if mode == "no-authority" {
				authorize = nil
			}
			task, err := plan.submit(context.Background(), authorize)
			if (err == nil) != (mode == "accepted") {
				t.Fatal("submission result", err)
			}
			if mode == "accepted" && (task.ID != "task-id" || task.URL != "") {
				t.Fatal("unsafe task receipt")
			}
			if _, err := plan.submit(context.Background(), func(context.Context) error { return nil }); err == nil {
				t.Fatal("replayed plan")
			}
			if strings.HasPrefix(mode, "late-") || mode == "no-authority" || mode == "denied-authority" {
				if posts != 0 {
					t.Fatal("stale event submitted")
				}
			} else if posts != 1 {
				t.Fatal("wrong submission count", posts)
			}
		})
	}
}
