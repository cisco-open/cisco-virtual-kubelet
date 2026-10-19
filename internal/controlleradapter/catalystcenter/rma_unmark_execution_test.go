// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRMAUnmarkNativeLifecycle(t *testing.T) {
	for _, mode := range []string{"nonfaulty", "absent", "pending-task", "failed-task", "still-marked", "lost-receipt", "wrong-record", "already-unmarked", "workflow-before", "late-record", "late-workflow", "recreated-after", "workflow-after", "unknown-phase"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			ref, want, devices, records := rmaBindingFixture()
			want.action = rmaMark
			want.replacementSerial = ""
			now := time.Now().UTC().Truncate(time.Second)
			created := now.Add(-time.Hour).UnixMilli()
			records[0].CreatedAt = created
			posts := 0
			if mode == "wrong-record" {
				records[0].ID = "foreign"
			}
			if mode == "already-unmarked" {
				records[0].Status = "NON-FAULTY"
			}
			if mode == "workflow-before" {
				records[0].WorkflowID = "foreign-workflow"
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "PUT" && r.URL.Path == deviceReplacementPath {
					posts++
					body, err := io.ReadAll(r.Body)
					if err != nil || string(body) != `[{"id":"record","replacementStatus":"NON-FAULTY"}]` {
						t.Error("invalid unmark payload", string(body), err)
					}
					records[0].Status = "NON-FAULTY"
					switch mode {
					case "absent":
						records = []deviceReplacementRecord{}
					case "still-marked":
						records[0].Status = "MARKED-FOR-REPLACEMENT"
					case "recreated-after":
						records[0].CreatedAt++
					case "workflow-after":
						records[0].WorkflowID = "foreign-workflow"
					}
					if mode == "lost-receipt" {
						fmt.Fprint(w, `{"response":{}}`)
						return
					}
					fmt.Fprint(w, `{"response":{"taskId":"unmark-task"}}`)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected mutation")
					w.WriteHeader(500)
					return
				}
				var rows any
				switch r.URL.Path {
				case devicesPath:
					rows = devices
				case deviceReplacementPath:
					rows = records
				case taskPath + "unmark-task":
					task := map[string]any{"id": "unmark-task", "startTime": now.UnixMilli(), "endTime": now.UnixMilli(), "isError": mode == "failed-task", "errorCode": "FAILED"}
					if mode == "pending-task" {
						delete(task, "endTime")
					}
					rows = task
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			write, err := c.planDeviceReplacementUnmark(ctx, ref, want, "record")
			invalid := mode == "wrong-record" || mode == "already-unmarked" || mode == "workflow-before"
			if invalid {
				if err == nil || write != nil || posts != 0 {
					t.Fatal("unowned/progressed state allowed cleanup", err)
				}
				return
			}
			if err != nil || write == nil || posts != 0 || write.action != "Delete" {
				t.Fatal("bad unmark plan", err)
			}
			binding, err := parseRMAUnmarkResource(write.resource)
			if err != nil || binding.recordID != "record" || binding.createdAt != created {
				t.Fatal("lost retained native identity", err)
			}
			if mode == "late-record" {
				records[0].ID = "foreign"
			}
			if mode == "late-workflow" {
				records[0].WorkflowID = "foreign-workflow"
			}
			task, err := write.submit(ctx)
			if mode == "late-record" || mode == "late-workflow" {
				if err == nil || posts != 0 {
					t.Fatal("stale cleanup submitted")
				}
				return
			}
			if mode == "lost-receipt" {
				if err == nil || task.ID != "" || posts != 1 {
					t.Fatal("lost receipt accepted")
				}
				return
			}
			if err != nil || posts != 1 {
				t.Fatal(err)
			}
			op := &cvk.NetworkControllerConfigurationOperation{Phase: "Accepted", Section: "inventory", Action: "Delete", Resource: write.resource, TargetID: write.identity.target, TargetSerial: write.identity.serial, ParentID: write.identity.parent, TaskID: task.ID, SubmittedAt: &metav1.Time{Time: now}}
			if mode == "unknown-phase" {
				op.Phase = "Unknown"
			}
			done, err := c.observeDeviceReplacementUnmark(ctx, ref, want, op, now)
			success := mode == "nonfaulty" || mode == "absent"
			pending := mode == "pending-task" || mode == "still-marked"
			if done != success || (err == nil) != (success || pending) || posts != 1 {
				t.Fatal("incorrect unmark evidence", done, err)
			}
			// Observation never issues a second PUT, including an incomplete outcome.
			c.observeDeviceReplacementUnmark(ctx, ref, want, op, now)
			if posts != 1 {
				t.Fatal("observation replayed cleanup")
			}
		})
	}
}

func TestRMAUnmarkResourceValidation(t *testing.T) {
	scope := strings.Repeat("a", 64)
	valid := "rma-unmark:" + scope + ":record:123"
	if _, err := parseRMAUnmarkResource(valid); err != nil {
		t.Fatal(err)
	}
	for _, resource := range []string{"", valid + ":extra", "rma-unmark:bad:record:123", "rma-unmark:" + strings.ToUpper(scope) + ":record:123", "rma-unmark:" + scope + ":bad/id:123", "rma-unmark:" + scope + ":record:0", "rma-unmark:" + scope + ":record:-1", "rma-unmark:" + scope + ":record:0123", "rma-unmark:" + scope + ":record:999999999999999999999"} {
		if _, err := parseRMAUnmarkResource(resource); err == nil {
			t.Fatal("accepted malformed unmark binding", resource)
		}
	}
}
