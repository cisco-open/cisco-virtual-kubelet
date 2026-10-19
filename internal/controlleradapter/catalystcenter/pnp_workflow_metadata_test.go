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

func TestPnPWorkflowMetadataProjection(t *testing.T) {
	for _, mode := range []string{"valid", "unknown-state", "unfinished", "absent-times", "bad-image-id", "bad-config-id", "negative-time", "fractional-time", "unsafe-integer", "inverted-times", "duplicate-sequence", "oversized", "string-time", "work-item-state", "work-item-inverted", "work-item-fractional", "work-item-string-time", "work-item-limit", "work-item-total-limit"} {
		t.Run(mode, func(t *testing.T) {
			item := map[string]any{"state": "native-item-state", "startTime": 1631000000000, "endTime": 1631000001000, "command": "private-command", "outputStr": "private-output"}
			task := map[string]any{"state": "native-task-state", "type": "native-task-type", "taskSeqNo": 0, "startTime": 1631000000000, "endTime": 1631000001000, "name": "private-name", "workItemList": []any{item}}
			workflow := map[string]any{"_id": "workflow-id", "state": "native-state", "imageId": "image-id", "configId": "config-id", "startTime": 1631000000000, "endTime": 1631000002000, "lastupdateOn": 1631000002000, "description": "private-description", "tasks": []any{task}}
			switch mode {
			case "work-item-state":
				item["state"] = "future-item-state"
			case "work-item-inverted":
				item["endTime"] = 1
			case "work-item-fractional":
				item["startTime"] = 0.5
			case "work-item-string-time":
				item["startTime"] = "1631000000000"
			case "work-item-limit":
				task["workItemList"] = make([]map[string]any, 1001)
			case "work-item-total-limit":
				tasks := make([]map[string]any, 11)
				for i := range tasks {
					tasks[i] = map[string]any{"taskSeqNo": i, "workItemList": make([]map[string]any, 1000)}
				}
				workflow["tasks"] = tasks
			case "unknown-state":
				workflow["state"] = "future-state"
			case "unfinished":
				workflow["endTime"] = 0
				task["endTime"] = 0
			case "absent-times":
				delete(workflow, "startTime")
				delete(workflow, "endTime")
				delete(workflow, "lastupdateOn")
				delete(task, "startTime")
				delete(task, "endTime")
			case "bad-image-id":
				workflow["imageId"] = "bad/id"
			case "bad-config-id":
				workflow["configId"] = "bad/id"
			case "negative-time":
				workflow["startTime"] = -1
			case "fractional-time":
				task["startTime"] = 0.5
			case "unsafe-integer":
				workflow["lastupdateOn"] = 9007199254740992.0
			case "inverted-times":
				task["endTime"] = 1
			case "duplicate-sequence":
				workflow["tasks"] = []any{task, task}
			case "oversized":
				tasks := make([]any, 1001)
				for i := range tasks {
					tasks[i] = map[string]any{}
				}
				workflow["tasks"] = tasks
			case "string-time":
				workflow["startTime"] = "1631000000000"
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != pnpDevicesPath {
					t.Error("unexpected request")
					w.WriteHeader(400)
					return
				}
				row := pnpFixture("device-id", "FOC12345678")
				row["workflow"], row["systemWorkflow"], row["systemResetWorkflow"] = workflow, workflow, workflow
				row["workflowParameters"] = map[string]any{"configList": []any{map[string]any{"configId": "config-id", "configParameters": []any{map[string]any{"key": "private-key", "value": "private-value"}}}}, "licenseLevel": "private-license", "topOfStackSerialNumber": "private-stack"}
				json.NewEncoder(w).Encode([]any{row})
			})
			rows, err := c.pnpDevices(context.Background())
			valid := mode == "valid" || mode == "unknown-state" || mode == "unfinished" || mode == "absent-times" || mode == "work-item-state"
			if (err == nil) != valid {
				t.Fatal("workflow metadata result", err)
			}
			if !valid {
				return
			}
			for _, got := range []*pnpWorkflowIdentity{rows[0].Workflow, rows[0].SystemWorkflow, rows[0].ResetWorkflow} {
				if got == nil || got.ID != "workflow-id" || got.ImageID != "image-id" || got.ConfigID != "config-id" || len(got.Tasks) != 1 {
					t.Fatal("lost safe workflow metadata")
				}
				if len(got.Tasks[0].WorkItems) != 1 || got.Tasks[0].WorkItems[0].StartTime == nil || got.Tasks[0].WorkItems[0].EndTime == nil {
					t.Fatal("lost safe work-item metadata")
				}
				expectedItemState := "native-item-state"
				if mode == "work-item-state" {
					expectedItemState = "future-item-state"
				}
				if got.Tasks[0].WorkItems[0].State != expectedItemState {
					t.Fatal("invented work-item semantics")
				}
				if mode == "unknown-state" && got.State != "future-state" {
					t.Fatal("invented native state mapping")
				}
				if mode == "absent-times" && (got.StartTime != nil || got.EndTime != nil || got.UpdatedAt != nil) {
					t.Fatal("invented timing evidence")
				}
				if mode == "unfinished" && (got.EndTime == nil || *got.EndTime != 0) {
					t.Fatal("lost unfinished sentinel")
				}
			}
			encoded, _ := json.Marshal(rows)
			if strings.Contains(string(encoded), "private-") || strings.Contains(string(encoded), "outputStr") || strings.Contains(string(encoded), "command") {
				t.Fatal("sensitive workflow data retained")
			}
		})
	}
}
