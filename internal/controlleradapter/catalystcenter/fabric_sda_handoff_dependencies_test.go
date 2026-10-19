// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFabricSDAHandoffDependencies(t *testing.T) {
	for _, mode := range []string{"valid", "missing-cp", "duplicate-serial", "late-local", "changed-cp", "changed-provision", "missing-member", "changed-transit", "exec-create", "exec-existing", "exec-conflict", "exec-denied", "exec-missing-task", "exec-error", "exec-async", "exec-drift", "exec-readback-missing", "exec-readback-conflict", "exec-readback-cp", "dispatch-valid", "dispatch-denied", "dispatch-lost", "dispatch-revoked", "dispatch-recovery-public", "dispatch-recovery-public-unknown", "dispatch-recovery-public-replaced", "dispatch-recovery-public-missing", "dispatch-recovery-public-denied", "dispatch-recovery-public-source", "dispatch-recovery-valid", "dispatch-recovery-unknown", "dispatch-recovery-running", "dispatch-recovery-failed", "dispatch-recovery-missing", "dispatch-recovery-denied", "dispatch-recovery-no-task", "dispatch-recovery-node", "dispatch-recovery-incomplete", "dispatch-recovery-publish-denied", "dispatch-recovery-publish-lost", "dispatch-recovery-publish-incomplete"} {
		t.Run(mode, func(t *testing.T) {
			flag := true
			border := &fabricBorderSettings{Types: []string{"LAYER_3"}, Layer3: &fabricBorderLayer3{ASN: "65000", DefaultExit: &flag, ImportExternal: &flag}}
			want := fabricSDAHandoffDeploymentIntent{handoff: fabricSDAHandoffIntent{Device: "border", Transit: "SDA"}, device: fabricDeviceIntent{inventory: inventoryDeviceIntent{reference: configurationDeviceReference{Name: "border"}, site: "Global/Lab/Building"}, state: "PROVISION", fabricSite: "Global/Lab", roles: []string{"BORDER_NODE"}, border: border}}
			changed := false
			reads := 0
			posts := 0
			var afterPost func()
			now := time.Now().UTC().Truncate(time.Second)
			desired := fabricSDAHandoff{DeviceID: "device", FabricID: "fabric", TransitID: "transit"}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "POST" && r.URL.Path == fabricSDAHandoffsPath {
					posts++
					if afterPost != nil {
						afterPost()
					}
					var body []fabricSDAHandoff
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || !reflect.DeepEqual(body[0], desired) {
						t.Error("incorrect native create payload")
					}
					switch mode {
					case "exec-missing-task":
						fmt.Fprint(w, `{"response":{}}`)
						return
					case "exec-error", "dispatch-lost":
						w.WriteHeader(400)
					case "exec-async":
						w.WriteHeader(202)
					}
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					return
				}
				if r.Method != "GET" {
					t.Error("dependency collection mutated controller")
					w.WriteHeader(400)
					return
				}
				var rows any
				switch r.URL.Path {
				case taskPath + "task/tree":
					rows = []map[string]any{{"id": "task", "isError": false, "startTime": now.Add(-time.Second).UnixMilli(), "endTime": now.UnixMilli()}, {"id": "child", "parentId": "task", "rootId": "task", "isError": mode == "dispatch-recovery-failed", "startTime": now.Add(-time.Second).UnixMilli(), "endTime": now.UnixMilli()}}
					if mode == "dispatch-recovery-running" {
						rows.([]map[string]any)[1]["endTime"] = 0
					}

				case sitesPath:
					rows = sampleSites()
				case fabricSitesPath:
					rows = []fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &flag}}
				case assignedDevicesPath:
					rows = []map[string]string{{"deviceId": "device", "siteId": "building"}}
				case wiredProvisionPath:
					id := "provision"
					if changed && (mode == "changed-provision" || mode == "exec-drift") {
						id = "replacement"
					}
					rows = []wiredProvisionedDevice{{ID: id, DeviceID: "device", SiteID: "building"}}
				case fabricDevicesPath:
					members := []fabricDeviceConfiguration{{ID: "member", NetworkDeviceID: "device", FabricID: "fabric", Roles: []string{"BORDER_NODE"}, Border: border}}
					if mode == "missing-member" {
						members = nil
					}
					rows = members
				case devicesPath:
					reads++
					inventory := []Device{{ID: "device", Hostname: "border", Serial: "SERIAL-BORDER", ManagementIP: "198.51.100.101"}, {ID: "cp", Hostname: "cp", Serial: "SERIAL-CP", ManagementIP: "198.51.100.102"}}
					if mode == "missing-cp" {
						inventory = inventory[:1]
					}
					if mode == "duplicate-serial" {
						inventory[1].Serial = "serial-border"
					}
					if (changed && mode == "changed-cp") || (posts > 0 && mode == "exec-readback-cp") {
						inventory[1].Serial = "replacement-cp"
					}
					if mode == "late-local" && reads%2 == 0 {
						inventory[0].Serial = "replacement-border"
					}
					rows = inventory
				case fabricTransitsPath:
					id := "transit"
					if changed && mode == "changed-transit" {
						id = "replacement"
					}
					rows = []fabricTransit{{ID: id, Name: "SDA", Type: "SDA_LISP_PUB_SUB_TRANSIT", SDA: &sdaTransitSettings{Multicast: &flag, ControlPlanes: []string{"cp"}}}}
				case fabricSDAHandoffsPath:
					handoffs := []fabricSDAHandoff{}
					if mode == "exec-existing" || mode == "exec-conflict" || posts > 0 && mode != "exec-readback-missing" && mode != "dispatch-recovery-missing" {
						row := desired
						if mode == "exec-conflict" || mode == "exec-readback-conflict" {
							row.TransitID = "other"
						}
						handoffs = append(handoffs, row)
					}
					rows = handoffs
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			if strings.HasPrefix(mode, "dispatch-") {
				testFabricSDAHandoffDispatch(t, c, want.handoff, mode, &afterPost)
				expected := 1
				if mode == "dispatch-denied" {
					expected = 0
				}
				if posts != expected {
					t.Fatal("unexpected dispatch count", posts)
				}
				return
			}
			if strings.HasPrefix(mode, "exec-") {
				ctx := context.Background()
				if _, err := c.planGuardedFabricSDAHandoffCreate(ctx, want, nil); err == nil {
					t.Fatal("missing guard accepted")
				}
				write, err := c.planGuardedFabricSDAHandoffCreate(ctx, want, func(context.Context) error {
					if mode == "exec-denied" {
						return errors.New("fixture denied")
					}
					return nil
				})
				if mode == "exec-existing" {
					if err != nil || write != nil || posts != 0 {
						t.Fatal("existing handoff rewritten", err)
					}
					return
				}
				if mode == "exec-conflict" {
					if err == nil || write != nil || posts != 0 {
						t.Fatal("conflict accepted")
					}
					return
				}
				if err != nil || write == nil {
					t.Fatal("create plan", err)
				}
				changed = true
				task, err := write.submit(ctx)
				success := mode == "exec-create" || strings.HasPrefix(mode, "exec-readback-")
				if (err == nil) != success || success && task.ID != "task" {
					t.Fatal("submission outcome", err)
				}
				expected := 1
				if mode == "exec-denied" || mode == "exec-drift" {
					expected = 0
				}
				if posts != expected {
					t.Fatal("unexpected mutation count", posts)
				}
				if _, err := write.submit(ctx); err == nil || posts != expected {
					t.Fatal("submission replayed")
				}
				if success {
					op := &cvk.NetworkControllerConfigurationOperation{Phase: "Verifying", Action: "Create", TaskID: task.ID, Resource: write.resource, TargetID: write.identity.target, ParentID: write.identity.parent}
					verified, err := c.verifyFabricSDAHandoffCreate(ctx, want, op)
					if mode == "exec-readback-conflict" || mode == "exec-readback-cp" {
						if err == nil || verified {
							t.Fatal("changed recovery evidence accepted")
						}
					} else if err != nil || verified != (mode != "exec-readback-missing") {
						t.Fatal("native verification outcome", verified, err)
					}
					op.Phase = "Accepted"
					if verified, err := c.verifyFabricSDAHandoffCreate(ctx, want, op); err == nil || verified {
						t.Fatal("invalid phase verified")
					}
				}
				return
			}
			first, err := c.fabricSDAHandoffDependencies(context.Background(), want)
			invalid := mode == "missing-cp" || mode == "duplicate-serial" || mode == "late-local" || mode == "missing-member"
			if invalid {
				if err == nil || first != nil {
					t.Fatal("invalid footprint accepted")
				}
				return
			}
			if err != nil || len(first.physical) != 2 {
				t.Fatal("incomplete footprint", err)
			}
			changed = true
			second, err := c.fabricSDAHandoffDependencies(context.Background(), want)
			if err != nil {
				t.Fatal(err)
			}
			if (first.digest == second.digest) != (mode == "valid") {
				t.Fatal("wrong dependency change detection")
			}
		})
	}
}
