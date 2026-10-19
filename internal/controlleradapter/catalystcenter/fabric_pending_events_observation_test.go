// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFabricPendingEventObservation(t *testing.T) {
	for _, mode := range []string{"settled", "running", "event-pending", "unrelated-event", "failed-task", "foreign-task", "old-task", "missing-task", "wrong-resource", "wrong-site", "wrong-fabric", "late-event", "late-site", "tree-running", "tree-failed"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC()
			eventReads, siteReads := 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("observation mutated controller")
					return
				}
				switch r.URL.Path {
				case sitesPath:
					siteReads++
					sites := sampleSites()
					if mode == "late-site" && siteReads > 1 {
						sites[2].ID = "replacement-site"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": sites})
				case fabricSitesPath:
					pubsub := true
					siteID := "building"
					if mode == "late-site" && siteReads > 1 {
						siteID = "replacement-site"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": []fabricSite{{ID: "fabric-id", SiteID: siteID, Authentication: "Closed Authentication", PubSub: &pubsub}}})
				case fabricDevicesPath:
					if r.URL.Query().Get("fabricId") != "fabric-id" {
						t.Error("lost membership scope")
					}
					json.NewEncoder(w).Encode(map[string]any{"response": []fabricDeviceMembership{{ID: "member-id", NetworkDeviceID: "device-id", FabricID: "fabric-id", Roles: []string{"EDGE_NODE"}}}})
				case devicesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []Device{{ID: "device-id", Serial: "FOC12345678"}}})
				case fabricPendingEventsPath:
					eventReads++
					rows := []fabricPendingEvent{}
					if mode == "event-pending" || mode == "late-event" && eventReads > 1 {
						rows = append(rows, fabricPendingEvent{ID: "event-id", FabricID: "fabric-id"})
					}
					if mode == "unrelated-event" {
						rows = append(rows, fabricPendingEvent{ID: "other-event", FabricID: "fabric-id"})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case taskPath + "task-id/tree":
					end := now.UnixMilli()
					if mode == "tree-running" {
						end = 0
					}
					json.NewEncoder(w).Encode(map[string]any{"response": []map[string]any{
						{"id": "task-id", "rootId": "task-id", "startTime": now.UnixMilli(), "endTime": now.UnixMilli(), "isError": false},
						{"id": "child", "rootId": "task-id", "parentId": "task-id", "startTime": now.UnixMilli(), "endTime": end, "isError": mode == "tree-failed"},
					}})
				case taskPath + "task-id":
					id, start, end := "task-id", now.UnixMilli(), now.UnixMilli()
					if mode == "foreign-task" {
						id = "foreign-id"
					}
					if mode == "old-task" {
						start = now.Add(-24 * time.Hour).UnixMilli()
					}
					if mode == "running" {
						end = 0
					}
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": id, "startTime": start, "endTime": end, "isError": mode == "failed-task", "failureReason": "private-error"}})
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			want := fabricSiteIntent{path: "Global/Lab/Building", authentication: "Closed Authentication", pubsub: true}
			event := fabricPendingEvent{ID: "event-id", FabricID: "fabric-id"}
			receipt := fabricPendingEventReceipt{siteID: "building", taskID: "task-id", event: event, submitted: now, resource: pendingFabricEventResource(want.path, "building", event)}
			switch mode {
			case "missing-task":
				receipt.taskID = ""
			case "wrong-resource":
				receipt.resource += "x"
			case "wrong-site":
				receipt.siteID = "foreign-site"
				receipt.resource = pendingFabricEventResource(want.path, receipt.siteID, event)
			case "wrong-fabric":
				receipt.event.FabricID = "foreign-fabric"
				receipt.resource = pendingFabricEventResource(want.path, receipt.siteID, receipt.event)
			}
			got, err := c.observePendingFabricEvent(context.Background(), want, receipt, now)
			valid := mode == "tree-running" || mode == "settled" || mode == "running" || mode == "event-pending" || mode == "unrelated-event"
			if (err == nil) != valid {
				t.Fatal("observation result", err)
			}
			if err != nil && strings.Contains(err.Error(), "private-error") {
				t.Fatal("native error leaked")
			}
			if valid && (got.taskComplete != (mode != "running" && mode != "tree-running") || got.eventPending != (mode == "event-pending")) {
				t.Fatal("task and event evidence conflated")
			}
		})
	}
}
