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
	"testing"
)

func TestFabricPortChannelNativeExecution(t *testing.T) {
	for _, mode := range []string{"create", "existing", "missing-protocol", "denied", "changed-port", "changed-intent", "lost", "unauthorized", "async", "missing-task", "readback-missing", "readback-foreign"} {
		t.Run(mode, func(t *testing.T) {
			fixture := "created"
			if mode == "existing" {
				fixture = "existing"
			}
			if mode == "changed-port" {
				fixture = mode
			}
			if mode == "readback-foreign" {
				fixture = "foreign-channel"
			}
			posts := 0
			var revised *bool
			c, want, revision := portChannelDependencyFixture(t, fixture, func(w http.ResponseWriter, r *http.Request) {
				posts++
				*revised = true
				var body []map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body[0]["id"] != nil || body[0]["networkDeviceId"] != "edge-id" || body[0]["fabricId"] != "fabric" || body[0]["protocol"] != "LACP" {
					t.Error("incorrect native payload")
				}
				switch mode {
				case "lost":
					w.WriteHeader(500)
					return
				case "unauthorized":
					w.WriteHeader(401)
					return
				case "async":
					w.WriteHeader(202)
				case "missing-task":
					fmt.Fprint(w, `{"response":{}}`)
					return
				}
				fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
			})
			revised = revision
			protocol := "LACP"
			if mode != "missing-protocol" {
				want.channel.Protocol = &protocol
			}
			ctx := context.Background()
			if _, err := c.planGuardedFabricPortChannelCreate(ctx, want, nil); err == nil {
				t.Fatal("missing guard accepted")
			}
			plan, err := c.planGuardedFabricPortChannelCreate(ctx, want, func(context.Context) error {
				if mode == "denied" {
					return errors.New("fixture denied")
				}
				return nil
			})
			if mode == "missing-protocol" {
				if err == nil || plan != nil || posts != 0 {
					t.Fatal("invented protocol default")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "existing" {
				if plan != nil || posts != 0 {
					t.Fatal("unowned existing channel rewritten")
				}
				return
			}
			if plan == nil {
				t.Fatal("missing create plan")
			}
			if mode == "changed-port" {
				*revised = true
			}
			if mode == "changed-intent" {
				protocol = "ON"
			}
			task, err := plan.submit(ctx)
			good := mode == "create" || mode == "readback-missing" || mode == "readback-foreign"
			if (err == nil) != good {
				t.Fatal("submission outcome", err)
			}
			expected := 1
			if mode == "denied" || mode == "changed-port" || mode == "changed-intent" {
				expected = 0
			}
			if posts != expected {
				t.Fatal("submission count", posts)
			}
			if _, err := plan.submit(ctx); err == nil || posts != expected {
				t.Fatal("uncertain operation replayed")
			}
			if !good {
				return
			}
			if mode == "readback-missing" {
				*revised = false
			}
			op := &cvk.NetworkControllerConfigurationOperation{Phase: "Verifying", Action: "Create", TaskID: task.ID, Resource: plan.resource, TargetID: plan.identity.target, ParentID: plan.identity.parent}
			id, err := c.verifyFabricPortChannelCreate(ctx, want, op)
			if mode == "readback-foreign" {
				if err == nil || id != "" {
					t.Fatal("unrelated native change ignored")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (id == "native-channel") != (mode == "create") {
				t.Fatal("incorrect completion observation", id)
			}
			op.TargetID = "replaced"
			if _, err := c.verifyFabricPortChannelCreate(ctx, want, op); err == nil {
				t.Fatal("foreign operation accepted")
			}
		})
	}
}
