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
)

func TestFabricPendingEventsSnapshot(t *testing.T) {
	for _, mode := range []string{"stable", "empty", "missing-fabric", "drift", "late-site", "late-fabric", "late-event", "late-addition", "missing-device", "serial-alias", "late-member", "late-serial", "late-role"} {
		t.Run(mode, func(t *testing.T) {
			late := false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("snapshot mutated controller")
					return
				}
				siteID, fabricID := "building", "fabric-id"
				if late && mode == "late-site" {
					siteID = "replacement-site"
				}
				if late && mode == "late-fabric" {
					fabricID = "replacement-fabric"
				}
				switch r.URL.Path {
				case sitesPath:
					sites := sampleSites()
					sites[2].ID = siteID
					json.NewEncoder(w).Encode(map[string]any{"response": sites})
				case fabricSitesPath:
					pubsub := true
					if mode == "drift" {
						pubsub = false
					}
					rows := []fabricSite{{ID: fabricID, SiteID: siteID, Authentication: "Closed Authentication", PubSub: &pubsub}}
					if mode == "missing-fabric" {
						rows = []fabricSite{}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case fabricDevicesPath:
					if r.URL.Query().Get("fabricId") != fabricID {
						t.Error("lost membership scope")
					}
					members := []fabricDeviceMembership{{ID: "member-id", NetworkDeviceID: "device-id", FabricID: fabricID, Roles: []string{"EDGE_NODE"}}, {ID: "border-member", NetworkDeviceID: "border-id", FabricID: fabricID, Roles: []string{"BORDER_NODE"}}}
					if late && mode == "late-member" {
						members = members[:1]
					}
					if late && mode == "late-role" {
						members[0].Roles = []string{"CONTROL_PLANE_NODE"}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": members})
				case devicesPath:
					devices := []Device{{ID: "device-id", Serial: "FOC12345678"}, {ID: "border-id", Serial: "FOC87654321"}}
					if mode == "missing-device" {
						devices = devices[:1]
					}
					if mode == "serial-alias" {
						devices = append(devices, Device{ID: "alias-id", Serial: "foc12345678"})
					}
					if late && mode == "late-serial" {
						devices[0].Serial = "FOC11223344"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": devices})
				case fabricPendingEventsPath:
					if r.URL.Query().Get("fabricId") != fabricID {
						t.Error("lost fabric scope")
					}
					rows := []fabricPendingEvent{{ID: "event-id", FabricID: fabricID}}
					if mode == "empty" {
						rows = []fabricPendingEvent{}
					}
					if late && mode == "late-event" {
						rows[0].ID = "replacement-event"
					}
					if late && mode == "late-addition" {
						rows = append(rows, fabricPendingEvent{ID: "new-event", FabricID: fabricID})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			want := fabricSiteIntent{path: "Global/Lab/Building", authentication: "Closed Authentication", pubsub: true}
			before, err := c.pendingFabricEventsSnapshot(context.Background(), want)
			valid := mode != "drift" && mode != "missing-fabric" && mode != "missing-device" && mode != "serial-alias"
			if (err == nil) != valid {
				t.Fatal("snapshot result", err)
			}
			if !valid {
				return
			}
			if before.site.actual.ID != "fabric-id" || before.site.siteID != "building" {
				t.Fatal("lost native identity")
			}
			if len(before.members) != 2 || len(before.devices) != 2 || before.devices[0].ID != "border-id" || before.devices[1].ID != "device-id" {
				t.Fatal("incomplete physical footprint")
			}
			late = true
			err = c.revalidatePendingFabricEvents(context.Background(), want, before)
			if (err != nil) != strings.HasPrefix(mode, "late-") {
				t.Fatal("revalidation result", err)
			}
		})
	}
}
