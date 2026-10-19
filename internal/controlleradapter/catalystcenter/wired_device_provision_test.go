// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWiredDeviceProvision(t *testing.T) {
	for _, mode := range []string{"create", "reprovision", "noop", "reprovision-absent", "wrong-site", "late-native", "late-device", "lost-receipt", "task-failed", "task-running", "task-stale", "missing-readback", "replaced-readback", "unknown", "unassigned", "wireless"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now()
			submitted := metav1.NewTime(now.Add(-time.Minute))
			intents, err := parseFabricDeviceIntents([]byte(fabricEdgeIntentModel))
			if err != nil {
				t.Fatal(err)
			}
			want := intents[0].inventory
			state := "PROVISION"
			if strings.HasPrefix(mode, "reprovision") || mode == "replaced-readback" {
				state = "REPROVISION"
			}
			present := mode == "noop" || mode == "reprovision" || mode == "wrong-site" || mode == "replaced-readback"
			late, recovering := false, false
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path == wiredProvisionPath && r.Method != "GET" {
					writes++
					method := "POST"
					if state == "REPROVISION" {
						method = "PUT"
					}
					if r.Method != method {
						t.Errorf("wrong method %s", r.Method)
					}
					var body []wiredProvisionedDevice
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 {
						t.Error("invalid body")
						w.WriteHeader(500)
						return
					}
					if body[0].SiteID != "building" || body[0].DeviceID != "edge-id" || (body[0].ID != "") != (state == "REPROVISION") {
						t.Error("wrong payload", body)
					}
					present = true
					if mode == "lost-receipt" {
						fmt.Fprint(w, `{"response":{}}`)
						return
					}
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					return
				}
				if r.Method != "GET" {
					t.Errorf("unexpected mutation %s", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				if r.URL.Path == taskPath+"task" {
					start, end := submitted.UnixMilli(), now.UnixMilli()
					if mode == "task-stale" {
						start = submitted.Add(-time.Hour).UnixMilli()
					}
					if mode == "task-running" {
						end = 0
					}
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "task", "startTime": start, "endTime": end, "isError": mode == "task-failed"}})
					return
				}
				var rows any
				switch r.URL.Path {
				case devicesPath:
					device := Device{ID: "edge-id", Hostname: "edge", Serial: "FOC12345678", Family: "Switches and Hubs", Reachability: "Reachable"}
					if mode == "wireless" {
						device.Family = "Wireless Controller"
					}
					if late && mode == "late-device" {
						device.Serial = "FOC99999999"
					}
					rows = []Device{device}
				case sitesPath:
					rows = sampleSites()
				case assignedDevicesPath:
					assignments := []map[string]string{{"deviceId": "edge-id", "siteId": "building"}}
					if mode == "unassigned" {
						assignments = []map[string]string{}
					}
					rows = assignments
				case wiredProvisionPath:
					if r.URL.Query().Get("networkDeviceId") != "edge-id" {
						t.Error("unfiltered provision lookup")
					}
					provisioned := []wiredProvisionedDevice{}
					if present || late && mode == "late-native" {
						site, id := "building", "provisioned"
						if mode == "wrong-site" {
							site = "area"
						}
						if recovering && mode == "replaced-readback" {
							id = "replacement"
						}
						provisioned = append(provisioned, wiredProvisionedDevice{ID: id, SiteID: site, DeviceID: "edge-id"})
					}
					if recovering && mode == "missing-readback" {
						provisioned = []wiredProvisionedDevice{}
					}
					rows = provisioned
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			write, err := c.planWiredDeviceProvision(ctx, want, state)
			if mode == "wrong-site" || mode == "reprovision-absent" || mode == "unassigned" || mode == "wireless" {
				if err == nil || write != nil {
					t.Fatal("invalid plan", write, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "noop" {
				if write != nil {
					t.Fatal("noop produced write")
				}
				return
			}
			if write == nil {
				t.Fatal("missing plan")
			}
			late = true
			task, err := write.submit(ctx)
			if strings.HasPrefix(mode, "late-") {
				if err == nil || writes != 0 {
					t.Fatal(err, writes)
				}
				return
			}
			if mode == "lost-receipt" {
				if err == nil || writes != 1 {
					t.Fatal(err, writes)
				}
				return
			}
			if err != nil || task.ID != "task" || writes != 1 {
				t.Fatal(task, err, writes)
			}
			op := &cvk.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, Phase: "Verifying", TaskID: task.ID, SubmittedAt: &submitted, TargetID: write.identity.target, ParentID: write.identity.parent, TargetSerial: write.identity.serial}
			if mode == "unknown" {
				op.Phase = "Unknown"
			}
			recovering = true
			verified, err := c.verifyWiredDeviceProvision(ctx, want, state, op, now)
			success := mode == "create" || mode == "reprovision"
			if verified != success || success && err != nil {
				t.Fatal("wrong verification", verified, err)
			}
			if writes != 1 {
				t.Fatal("verification replayed mutation")
			}
		})
	}
}

func TestWiredProvisionObservationRejectsInvalid(t *testing.T) {
	for _, mode := range []string{"empty", "valid", "duplicate", "wrong-device", "missing-id", "missing-site", "null"} {
		t.Run(mode, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != wiredProvisionPath || r.URL.Query().Get("networkDeviceId") != "edge" {
					t.Error("wrong observation request")
				}
				rows := []wiredProvisionedDevice{{ID: "row", DeviceID: "edge", SiteID: "site"}}
				switch mode {
				case "empty":
					rows = []wiredProvisionedDevice{}
				case "null":
					rows = nil
				case "duplicate":
					rows = append(rows, wiredProvisionedDevice{ID: "row2", DeviceID: "edge", SiteID: "other"})
				case "wrong-device":
					rows[0].DeviceID = "other"
				case "missing-id":
					rows[0].ID = ""
				case "missing-site":
					rows[0].SiteID = ""
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			_, exists, err := c.wiredProvisionedDevice(context.Background(), "edge")
			if mode == "empty" {
				if exists || err != nil {
					t.Fatal(exists, err)
				}
			} else if mode == "valid" {
				if !exists || err != nil {
					t.Fatal(exists, err)
				}
			} else if err == nil || exists {
				t.Fatal("invalid evidence accepted", exists, err)
			}
		})
	}
}
