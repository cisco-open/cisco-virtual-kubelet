// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestConfigurationReceiptCompletion(t *testing.T) {
	for _, mode := range []string{"generic", "deployment", "pending", "error", "wrong-phase", "wrong-section", "missing-time", "future-time", "missing-receipt"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			submitted := metav1.NewTime(now.Add(-time.Minute))
			generic, deployment, observer := 0, 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("completion mutated API")
					w.WriteHeader(500)
					return
				}
				switch r.URL.Path {
				case taskPath + deploymentFixtureID:
					generic++
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": deploymentFixtureID, "startTime": submitted.UnixMilli(), "endTime": now.UnixMilli(), "isError": false}})
				case templateDeployStatusPath + deploymentFixtureID:
					deployment++
					if mode == "error" {
						w.WriteHeader(404)
						return
					}
					status := "SUCCESS"
					if mode == "pending" {
						status = "IN_PROGRESS"
					}
					json.NewEncoder(w).Encode(templateDeploymentStatus{ID: deploymentFixtureID, Project: "Lab", Name: "Baseline", Version: "2", Status: status, Start: submitted.Format(time.RFC3339), End: now.Format(time.RFC3339), Devices: []templateDeploymentDeviceStatus{{DeviceID: "device", Status: "SUCCESS", Start: submitted.Format(time.RFC3339), End: now.Format(time.RFC3339)}}})
				default:
					t.Error("wrong status path")
					w.WriteHeader(404)
				}
			})
			op := &cvk.NetworkControllerConfigurationOperation{Phase: "Accepted", Section: "inventory", TaskID: deploymentFixtureID, SubmittedAt: &submitted}
			plan := configurationApplyPlan{section: "inventory"}
			if mode != "generic" {
				plan.observeCompletion = func(ctx context.Context, copy *cvk.NetworkControllerConfigurationOperation, at time.Time) (bool, error) {
					observer++
					copy.Resource = "changed-local-copy"
					return c.templateDeploymentComplete(ctx, templateDeploymentIdentity{deploymentID: copy.TaskID, deviceID: "device", project: "Lab", name: "Baseline", version: 2}, copy.SubmittedAt.Time, at)
				}
			}
			invalid := false
			switch mode {
			case "wrong-phase":
				op.Phase = "Unknown"
				invalid = true
			case "wrong-section":
				op.Section = "sites"
				invalid = true
			case "missing-time":
				op.SubmittedAt = nil
				invalid = true
			case "future-time":
				future := metav1.NewTime(now.Add(time.Minute))
				op.SubmittedAt = &future
				invalid = true
			case "missing-receipt":
				op.TaskID = ""
				invalid = true
			}
			done, err := plan.taskComplete(context.Background(), c, op, now)
			if invalid {
				if err == nil || done || observer+generic+deployment != 0 {
					t.Fatal("invalid journal observed")
				}
			} else {
				success := mode == "generic" || mode == "deployment"
				if done != success || (err == nil) != (success || mode == "pending") {
					t.Fatal("wrong completion result", mode, err)
				}
				if mode == "generic" {
					if generic != 1 || observer+deployment != 0 {
						t.Fatal("generic behavior changed")
					}
				} else if observer != 1 || deployment != 1 || generic != 0 {
					t.Fatal("specialized receipt fell back to generic task")
				}
			}
			if op.Resource != "" {
				t.Fatal("observer mutated retained journal")
			}
		})
	}
}
