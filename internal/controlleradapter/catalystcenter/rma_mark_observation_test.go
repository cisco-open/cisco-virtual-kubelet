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
	"testing"
	"time"
)

func TestRMAMarkObservation(t *testing.T) {
	for _, mode := range []string{"marked", "ready", "pending-task", "missing-record", "failed-task", "stale-task", "wrong-task", "old-record", "future-record", "missing-serial", "wrong-serial", "workflow", "failed-step", "replacement-selected", "replaced", "unknown-status", "changed-device", "late-workflow", "unknown-phase", "missing-task", "wrong-resource", "wrong-target", "wrong-target-serial", "graph"} {
		t.Run(mode, func(t *testing.T) {
			ref, want, devices, records := rmaBindingFixture()
			now := time.Now().UTC().Truncate(time.Millisecond)
			submitted := now.Add(-time.Minute)
			records[0].CreatedAt = submitted.UnixMilli()
			records[0].Status = "MARKED-FOR-REPLACEMENT"
			snapshot, err := bindDeviceReplacement(ref, want, devices, nil)
			if err != nil {
				t.Fatal(err)
			}
			resource, err := deviceReplacementMarkResource(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			op := &cvk.NetworkControllerConfigurationOperation{Phase: "Verifying", Section: "inventory", Action: "Create", Resource: resource, TargetID: "old-id", TargetSerial: "old-serial", TaskID: "mark-task", SubmittedAt: &metav1.Time{Time: submitted}}
			switch mode {
			case "ready":
				records[0].Status = "READY-FOR-REPLACEMENT"
			case "missing-record":
				records = []deviceReplacementRecord{}
			case "old-record":
				records[0].CreatedAt = submitted.Add(-time.Hour).UnixMilli()
			case "future-record":
				records[0].CreatedAt = now.Add(time.Hour).UnixMilli()
			case "missing-serial":
				records[0].FaultySerial = ""
			case "wrong-serial":
				records[0].FaultySerial = "OTHER"
			case "failed-step":
				records[0].WorkflowFailedStep = "CONFIGURE"
			case "workflow":
				records[0].WorkflowID = "foreign-workflow"
			case "replacement-selected":
				records[0].ReplacementSerial = "NEW-SERIAL"
			case "replaced":
				records[0].ReplacedAt = now.UnixMilli()
			case "unknown-status":
				records[0].Status = "NEW-NATIVE-STATUS"
			case "changed-device":
				devices[0].ID = "replacement-id"
			case "unknown-phase":
				op.Phase = "Unknown"
			case "missing-task":
				op.TaskID = ""
			case "wrong-resource":
				op.Resource = "rma-mark:other"
			case "wrong-target":
				op.TargetID = "other"
			case "wrong-target-serial":
				op.TargetSerial = "other"
			case "graph":
				op.DependencyVersions = dependencyJournalIdentity().dependencies
			}
			reads, posts := 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					posts++
					w.WriteHeader(500)
					return
				}
				var rows any
				switch r.URL.Path {
				case devicesPath:
					rows = devices
				case deviceReplacementPath:
					reads++
					if mode == "late-workflow" && reads > 1 {
						records[0].WorkflowID = "foreign-workflow"
					}
					rows = records
				case taskPath + "mark-task":
					task := map[string]any{"id": "mark-task", "isError": false, "startTime": submitted.UnixMilli(), "endTime": now.UnixMilli()}
					switch mode {
					case "pending-task":
						delete(task, "endTime")
					case "failed-task":
						task["isError"] = true
						task["errorCode"] = "FAILED"
					case "stale-task":
						task["startTime"] = submitted.Add(-time.Hour).UnixMilli()
					case "wrong-task":
						task["id"] = "other-task"
					}
					rows = task
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			evidence, err := c.observeDeviceReplacementMark(context.Background(), ref, want, op, now)
			success := mode == "marked" || mode == "ready"
			pending := mode == "pending-task" || mode == "missing-record"
			if posts != 0 || (err == nil) != (success || pending) || (evidence != nil) != success {
				t.Fatal("unexpected completion evidence", evidence, err, posts)
			}
			if success && (evidence.ID != "record" || reads != 2) {
				t.Fatal("missing native identity/fresh read")
			}
			if op.Phase != "Verifying" && mode != "unknown-phase" {
				t.Fatal("observer changed journal")
			}
		})
	}
}
