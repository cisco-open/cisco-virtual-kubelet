// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFabricEWLCOwnedReceipt(t *testing.T) {
	for _, mode := range []string{"verified", "running", "failed", "missing-task", "wrong-phase", "disabled", "unconverged", "late-settings", "wrong-resource", "missing-member", "changed-serial", "bad-group", "future-submission"} {
		t.Run(mode, func(t *testing.T) {
			var after atomic.Bool
			var observations, tasks atomic.Int32
			c, want := ewlcComposedFixture(t, "valid", func(w http.ResponseWriter, r *http.Request) bool {
				reply := func(value any) { json.NewEncoder(w).Encode(map[string]any{"response": value}) }
				if r.URL.Path == "/dna/intent/api/v1/sda/fabrics/fabric/switchWirelessSetting" {
					n := observations.Add(1)
					rolling := after.Load() && mode != "unconverged"
					if mode == "late-settings" && n > 2 {
						rolling = false
					}
					reply([]fabricEWLCSetting{{ID: "edge-id", Enabled: true, Rolling: fabricEWLCRolling{Enabled: rolling, Percentage: 25}}})
					return true
				}
				if r.URL.Path == taskPath+"owned-task/tree" {
					tasks.Add(1)
					end := int64(150000)
					if mode == "running" {
						end = 0
					}
					reply([]map[string]any{{"id": "owned-task", "startTime": 100000, "endTime": end, "isError": mode == "failed"}})
					return true
				}
				return false
			})
			want.settings.rolling = true
			deps, err := c.fabricEWLCFabricDependencies(context.Background(), want)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := fabricEWLCPayload(deps.target.ID, want.settings)
			if err != nil {
				t.Fatal(err)
			}
			targets := []contract.GroupTargetIdentity{}
			for _, device := range deps.foundation.physical {
				targets = append(targets, contract.GroupTargetIdentity{InventoryID: device.ID, PhysicalIdentity: device.Serial, Device: ops.ConfigurationReservationReference{Name: device.ID, UID: device.ID + "-device"}, Node: ops.ConfigurationReservationReference{Name: device.ID, UID: device.ID + "-node"}})
			}
			if mode == "missing-member" {
				targets = targets[:1]
			}
			if mode == "changed-serial" {
				targets[0].PhysicalIdentity = "foc99999999"
			}
			op, err := contract.WithGroupIdentity(&cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "config", ConfigUID: "config-uid", ConfigGeneration: 1, ControllerGeneration: 1, Section: "inventory", Action: "Update", Phase: "Prepared", Resource: fabricEWLCResource(deps, payload), TargetID: "edge-id", ParentID: "fabric"}, targets)
			if err != nil {
				t.Fatal(err)
			}
			op.Phase = "Verifying"
			op.TaskID = "owned-task"
			submitted := metav1.NewTime(time.Unix(100, 0))
			op.SubmittedAt = &submitted
			switch mode {
			case "missing-task":
				op.TaskID = ""
			case "wrong-phase":
				op.Phase = "Accepted"
			case "disabled":
				want.settings.enabled = false
			case "wrong-resource":
				op.Resource = "fabric-ewlc-update:" + strings.Repeat("b", 64)
				op.PhysicalGroup.Digest, err = contract.GroupOperationDigest(op, op.PhysicalGroup.Targets)
				if err != nil {
					t.Fatal(err)
				}
			case "bad-group":
				op.PhysicalGroup.Digest = strings.Repeat("c", 64)
			case "future-submission":
				future := metav1.NewTime(time.Unix(300, 0))
				op.SubmittedAt = &future
			}
			after.Store(true)
			receipt, err := c.verifiedFabricEWLCReceipt(context.Background(), want, op, time.Unix(200, 0))
			if mode != "verified" {
				if err == nil || receipt != nil {
					t.Fatal("unverified receipt escaped", receipt, err)
				}
				if (mode == "running" || mode == "failed") && observations.Load() != 1 {
					t.Fatal("readback before task completion")
				}
				return
			}
			if err != nil || receipt == nil || receipt.Section != "inventory" || receipt.Operation.Phase != "Verified" || op.Phase != "Verifying" || tasks.Load() != 1 || observations.Load() != 3 {
				t.Fatal(receipt, err, tasks.Load(), observations.Load())
			}
			receipt.Operation.PhysicalGroup.Targets[0].PhysicalIdentity = "changed"
			if op.PhysicalGroup.Targets[0].PhysicalIdentity == "changed" {
				t.Fatal("receipt aliases active journal")
			}
		})
	}
}
