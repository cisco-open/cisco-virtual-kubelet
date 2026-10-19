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

func TestTagCreateRequiresOriginalTaskIdentity(t *testing.T) {
	const id = "22f33fa0-e275-41c9-a50d-d7e5135adb8c"
	for _, mode := range []string{"valid", "pending", "failed", "foreign-task", "missing-time", "future-time", "wrong-service", "missing-result", "noncanonical-result", "foreign-result", "missing-native", "protected", "taskless", "nil-operation"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now()
			at := metav1.NewTime(now.Add(-time.Second))
			op := &cvk.NetworkControllerConfigurationOperation{Action: "Create", Resource: "selected", TaskID: "original-task", SubmittedAt: &at}
			body := map[string]any{"id": "original-task", "startTime": now.Add(-time.Second).UnixMilli(), "endTime": now.UnixMilli(), "isError": false, "serviceType": "Grouping Service", "data": id}
			switch mode {
			case "pending":
				delete(body, "endTime")
			case "failed":
				body["isError"] = true
			case "foreign-task":
				body["id"] = "foreign"
			case "missing-time":
				delete(body, "startTime")
			case "future-time":
				body["startTime"] = now.Add(time.Hour).UnixMilli()
			case "wrong-service":
				body["serviceType"] = "NCTP"
			case "missing-result":
				delete(body, "data")
			case "noncanonical-result":
				body["data"] = "created " + id
			case "foreign-result":
				body["data"] = "11f33fa0-e275-41c9-a50d-d7e5135adb8c"
			case "taskless":
				op.TaskID = ""
			case "nil-operation":
				op = nil
			}
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					writes++
					t.Error("verification mutated native state")
					w.WriteHeader(400)
					return
				}
				if r.URL.Path == taskPath+"original-task" {
					json.NewEncoder(w).Encode(map[string]any{"response": body})
					return
				}
				if r.URL.Path == tagsPath {
					rows := []map[string]any{}
					if mode != "missing-native" {
						rows = append(rows, map[string]any{"id": id, "name": "selected", "description": "desired", "systemTag": mode == "protected"})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
					return
				}
				t.Error("unexpected endpoint", r.URL.Path)
				w.WriteHeader(404)
			})
			e := &tagExecutor{client: c, desired: []desiredTag{{name: "selected", payload: map[string]any{"name": "selected", "description": "desired", "systemTag": false}}}}
			ok, err := e.verify(context.Background(), op)
			if mode == "valid" {
				if !ok || err != nil {
					t.Fatal("original task/native binding rejected", ok, err)
				}
			} else if ok {
				t.Fatal("unproven creation accepted", mode)
			}
			if mode == "pending" && err != nil {
				t.Fatal("pending original task should remain observable", err)
			}
			if writes != 0 {
				t.Fatal("verification dispatched")
			}
		})
	}
}
