// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestFabricPortAssignmentNativeExecution(t *testing.T) {
	for _, mode := range []string{"create", "undeclared", "denied", "changed-port", "lost", "unauthorized", "accepted-202", "missing-task", "readback-missing", "bystander-replaced"} {
		t.Run(mode, func(t *testing.T) {
			posts, guards := 0, 0
			created := false
			c, _, revised := portChannelDependencyFixture(t, "assignment/"+(mode), func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					posts++
					var body []map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body[0]["interfaceName"] != "GigabitEthernet1/0/1" || body[0]["networkDeviceId"] != "edge-id" {
						t.Error("wrong create payload")
					}
					created = true
					switch mode {
					case "lost":
						w.WriteHeader(500)
						return
					case "unauthorized":
						w.WriteHeader(401)
						return
					case "accepted-202":
						w.WriteHeader(202)
						return
					case "missing-task":
						fmt.Fprint(w, `{"response":{}}`)
						return
					}
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected method")
					w.WriteHeader(400)
					return
				}
				rows := []fabricPortAssignment{{ID: "bystander", FabricID: "fabric", DeviceID: "edge-id", Interface: "GigabitEthernet1/0/2", Type: "USER_DEVICE"}}
				if created && mode != "readback-missing" {
					rows = append(rows, fabricPortAssignment{ID: "created", FabricID: "fabric", DeviceID: "edge-id", Interface: "GigabitEthernet1/0/1", Type: "USER_DEVICE"})
				}
				if created && mode == "bystander-replaced" {
					rows[0].ID = "replacement"
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"response": rows}); err != nil {
					t.Error(err)
				}
			})
			_, wants, err := parseFabricPortAssignmentDeployment(portAssignmentTestModel(`[{"interfaces_range":"GigabitEthernet1/0/1-1/0/2","connected_device_type":"USER_DEVICE"}]`))
			if err != nil {
				t.Fatal(err)
			}
			port := "GigabitEthernet1/0/1"
			if mode == "undeclared" {
				port = "GigabitEthernet1/0/3"
			}
			write, err := c.planGuardedFabricPortAssignmentCreate(context.Background(), wants[0], port, func(context.Context) error {
				guards++
				if mode == "denied" {
					return errConfigurationDeviceAdmission
				}
				return nil
			})
			if mode == "undeclared" {
				if err == nil || write != nil {
					t.Fatal("undeclared create planned")
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal("missing create plan", err)
			}
			if mode == "changed-port" {
				*revised = true
			}
			task, err := write.submit(context.Background())
			success := mode == "create" || mode == "readback-missing" || mode == "bystander-replaced"
			if (err == nil) != success {
				t.Fatal("submission outcome", err)
			}
			if _, err := write.submit(context.Background()); err == nil {
				t.Fatal("submission replayed")
			}
			expected := 1
			if mode == "denied" || mode == "changed-port" {
				expected = 0
			}
			if posts != expected || guards > 1 {
				t.Fatal("native replay or guard bypass", posts, guards)
			}
			if !success {
				return
			}
			op := &cvk.NetworkControllerConfigurationOperation{Phase: "Verifying", Action: "Create", TaskID: task.ID, Resource: write.resource, TargetID: "edge-id", ParentID: "fabric"}
			id, err := c.verifyFabricPortAssignmentCreate(context.Background(), wants[0], port, op)
			if mode == "bystander-replaced" {
				if err == nil || id != "" {
					t.Fatal("unrelated replacement hidden")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (id == "created") != (mode == "create") {
				t.Fatal("incorrect create readback", id)
			}
			if mode == "create" {
				plan, err := c.planGuardedFabricPortAssignmentCreate(context.Background(), wants[0], port, func(context.Context) error { t.Fatal("existing assignment requested authority"); return nil })
				if err != nil || plan != nil {
					t.Fatal("compliance became adoption", err)
				}
			}
		})
	}
}
