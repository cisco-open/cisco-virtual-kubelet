// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"strings"
	"testing"
)

func TestBoundFabricDeviceCreate(t *testing.T) {
	for _, mode := range []string{"create", "zone", "late-parent", "late-peer", "late-target", "recovery-parent", "recovery-peer", "recovery-role", "recovery-missing", "receipt-missing", "missing-task", "unknown", "recovery-zone-parent", "caller-change"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			raw := fabricEdgeIntentModel
			zone := mode == "zone" || mode == "recovery-zone-parent"
			if zone {
				raw = strings.Replace(raw, `"name":"edge"`, `"name":"edge","fabric_zone":"Global/Lab/Building"`, 1)
			}
			intents, err := parseFabricDeviceIntents([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			late, recovery, created := false, false, false
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "POST" {
					if r.URL.Path != fabricDevicesPath {
						t.Errorf("unexpected POST %s", r.URL.Path)
						w.WriteHeader(404)
						return
					}
					writes++
					var body []fabricDeviceConfiguration
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body[0].NetworkDeviceID != "edge-id" || len(body[0].Roles) != 1 || body[0].Roles[0] != "EDGE_NODE" {
						t.Error("invalid native payload")
					}
					created = true
					if mode == "receipt-missing" {
						fmt.Fprint(w, `{"response":{}}`)
						return
					}
					fmt.Fprint(w, `{"response":{"taskId":"fabric-task"}}`)
					return
				}
				if r.Method != "GET" {
					t.Errorf("unexpected method %s", r.Method)
					w.WriteHeader(500)
					return
				}
				var rows any
				switch r.URL.Path {
				case devicesPath:
					devices := []Device{{ID: "edge-id", Hostname: "edge", Serial: "FOC12345678"}, {ID: "peer-id", Hostname: "peer", Serial: "FOC87654321"}}
					if late && mode == "late-peer" || recovery && mode == "recovery-peer" {
						devices[1].Serial = "FOC99999999"
					}
					if late && mode == "late-target" {
						devices[0].Serial = "FOC99999999"
					}
					rows = devices
				case sitesPath:
					rows = sampleSites()
				case fabricSitesPath:
					no := false
					id := "fabric"
					if late && mode == "late-parent" || recovery && mode == "recovery-parent" {
						id = "replacement"
					}
					rows = []fabricSite{{ID: id, SiteID: "area", Authentication: "Closed Authentication", PubSub: &no}}
				case fabricZonesPath:
					rows = []fabricZone{{ID: "zone", SiteID: "building", Authentication: "Closed Authentication"}}
				case wiredProvisionPath:
					rows = []wiredProvisionedDevice{{ID: "provisioned", DeviceID: "edge-id", SiteID: "building"}}
				case assignedDevicesPath:
					rows = []map[string]string{{"deviceId": "edge-id", "siteId": "building"}}
				case fabricDevicesPath:
					fid := r.URL.Query().Get("fabricId")
					members := []fabricDeviceConfiguration{{ID: "peer-row", NetworkDeviceID: "peer-id", FabricID: fid, Roles: []string{"EDGE_NODE"}}}
					parent := zone && fid == "fabric"
					if parent || created && !(recovery && mode == "recovery-missing") {
						roles := []string{"EDGE_NODE"}
						if recovery && mode == "recovery-role" {
							roles = []string{"CONTROL_PLANE_NODE"}
						}
						id := "edge-row"
						if parent && recovery && mode == "recovery-zone-parent" {
							id = "replaced-parent-member"
						}
						members = append(members, fabricDeviceConfiguration{ID: id, NetworkDeviceID: "edge-id", FabricID: fid, Roles: roles})
					}
					rows = members
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			write, err := c.planBoundFabricDeviceCreate(ctx, intents[0])
			if err != nil || write == nil {
				t.Fatal(write, err)
			}
			if len(write.resource) > 512 {
				t.Fatal("journal bound exceeded")
			}
			late = true
			if mode == "caller-change" {
				intents[0].roles[0] = "BORDER_NODE"
			}
			task, err := write.submit(ctx)
			if strings.HasPrefix(mode, "late-") {
				if err == nil || writes != 0 {
					t.Fatal("stale submission", err, writes)
				}
				return
			}
			if mode == "receipt-missing" {
				if err == nil || writes != 1 {
					t.Fatal("missing receipt accepted", err, writes)
				}
				return
			}
			if err != nil || writes != 1 || task.ID != "fabric-task" {
				t.Fatal(task, err, writes)
			}
			// Reparse the source: recovery uses recorded identity, not a captured plan.
			intents, err = parseFabricDeviceIntents([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			fid := "fabric"
			if zone {
				fid = "zone"
			}
			op := &cvk.NetworkControllerConfigurationOperation{Action: "Create", Phase: "Verifying", TaskID: task.ID, TargetID: "edge-id", ParentID: fid, Resource: write.resource}
			if mode == "unknown" {
				op.Phase = "Unknown"
			}
			if mode == "missing-task" {
				op.TaskID = ""
			}
			recovery = true
			verified, err := c.verifyBoundFabricDeviceCreate(ctx, intents[0], op)
			invalid := strings.HasPrefix(mode, "recovery-") || mode == "unknown" || mode == "missing-task"
			if invalid {
				if verified {
					t.Fatal("invalid recovery verified")
				}
			} else if err != nil || !verified {
				t.Fatal(verified, err)
			}
			if writes != 1 {
				t.Fatal("recovery replayed mutation")
			}
			if !invalid {
				noop, err := c.planBoundFabricDeviceCreate(ctx, intents[0])
				if err != nil || noop != nil {
					t.Fatal("converged plan is not a no-op", noop, err)
				}
			}
		})
	}
}
