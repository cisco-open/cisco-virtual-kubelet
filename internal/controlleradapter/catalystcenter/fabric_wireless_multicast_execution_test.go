// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"testing"
)

func TestFabricWirelessMulticastExecution(t *testing.T) {
	for _, mode := range []string{"enable", "disable", "noop", "no-guard", "denied", "stale-setting", "stale-membership", "lost-receipt", "unauthorized", "accepted-no-task", "missing-task", "pending", "changed-tag", "changed-fabric", "invalid-operation"} {
		t.Run(mode, func(t *testing.T) {
			desired := mode != "disable"
			enabled := !desired
			if mode == "noop" {
				enabled = desired
			}
			revised := false
			writes, guards := 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				fabric := "fabric"
				if revised && mode == "changed-fabric" {
					fabric = "replacement"
				}
				if r.Method == "PUT" {
					writes++
					if r.URL.Path != "/dna/intent/api/v1/sda/fabrics/fabric/wirelessMulticast" {
						t.Error("wrong mutation endpoint")
						w.WriteHeader(400)
						return
					}
					var body map[string]bool
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["multicastEnabled"] != desired {
						t.Error("incorrect payload")
						w.WriteHeader(400)
						return
					}
					enabled = desired
					if mode == "pending" {
						enabled = !desired
					}
					switch mode {
					case "lost-receipt":
						w.WriteHeader(500)
						return
					case "unauthorized":
						w.WriteHeader(401)
						return
					case "accepted-no-task":
						w.WriteHeader(202)
						return
					case "missing-task":
						reply(map[string]any{})
						return
					}
					reply(map[string]string{"taskId": "task"})
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected method")
					w.WriteHeader(400)
					return
				}
				switch r.URL.Path {
				case sitesPath:
					reply(sampleSites())
				case fabricSitesPath:
					b := false
					reply([]fabricSite{{ID: fabric, SiteID: "area", Authentication: "No Authentication", PubSub: &b}})
				case fabricDevicesPath:
					id := "member"
					if revised && mode == "stale-membership" {
						id = "replaced-member"
					}
					reply([]fabricDeviceMembership{{ID: id, NetworkDeviceID: "wlc", FabricID: fabric, Roles: []string{"WIRELESS_CONTROLLER_NODE"}}})
				case "/dna/intent/api/v1/sda/fabrics/fabric/vlanToSsids", "/dna/intent/api/v1/sda/fabrics/replacement/vlanToSsids":
					tag := "Staff"
					if revised && mode == "changed-tag" {
						tag = "Other"
					}
					reply([]fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Staff", SecurityGroup: &tag}}}})
				case "/dna/intent/api/v1/sda/fabrics/fabric/wirelessMulticast", "/dna/intent/api/v1/sda/fabrics/replacement/wirelessMulticast":
					reply(map[string]bool{"multicastEnabled": enabled})
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			ctx := context.Background()
			guard := func(context.Context) error {
				guards++
				if mode == "denied" {
					return errConfigurationDeviceAdmission
				}
				return nil
			}
			if mode == "no-guard" {
				guard = nil
			}
			write, err := c.planGuardedFabricWirelessMulticast(ctx, "Global/Lab", desired, guard)
			if mode == "no-guard" {
				if err == nil || write != nil || writes != 0 {
					t.Fatal("guard bypass")
				}
				return
			}
			if mode == "noop" {
				if err != nil || write != nil || writes != 0 {
					t.Fatal("noop mutated", err)
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal("plan", err)
			}
			if mode == "stale-setting" {
				enabled = desired
			}
			if mode == "stale-membership" {
				revised = true
			}
			task, err := write.submit(ctx)
			refused := mode == "denied" || mode == "stale-setting" || mode == "stale-membership"
			uncertain := mode == "lost-receipt" || mode == "unauthorized" || mode == "accepted-no-task" || mode == "missing-task"
			expected := 1
			if refused {
				expected = 0
			}
			if writes != expected || ((err != nil) != (refused || uncertain)) {
				t.Fatal("submit", err, writes)
			}
			if _, retryErr := write.submit(ctx); retryErr == nil || writes != expected {
				t.Fatal("submission replayed")
			}
			if refused || uncertain {
				return
			}
			if task.ID != "task" || guards != 1 {
				t.Fatal("receipt or guard", task, guards)
			}
			op := &cvk.NetworkControllerConfigurationOperation{Phase: "Verifying", Action: "Update", TaskID: task.ID, Resource: write.resource, TargetID: write.identity.target, ParentID: write.identity.parent}
			revised = true
			if mode == "invalid-operation" {
				op.TaskID = ""
			}
			verified, err := c.verifyFabricWirelessMulticast(ctx, "Global/Lab", desired, op)
			failed := mode == "changed-tag" || mode == "changed-fabric" || mode == "invalid-operation"
			if (err != nil) != failed || verified != (mode == "enable" || mode == "disable") {
				t.Fatal("verification", verified, err)
			}
		})
	}
}
