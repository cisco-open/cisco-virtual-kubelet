// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSiteSingularTaskCompletion(t *testing.T) {
	for _, stage := range []string{"area-create", "building-create", "floor-create", "building-update", "floor-update"} {
		for _, phase := range []string{"Accepted", "Unknown"} {
			for _, mode := range []string{"complete", "pending-child", "failed-child", "foreign-child", "unavailable", "partial", "missing-root"} {
				t.Run(stage+"/"+phase+"/"+mode, func(t *testing.T) {
					desired, _ := siteDispatchFixture(stage)
					now := time.Now().UTC()
					submitted := metav1.NewTime(now.Add(-time.Second))
					op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), Section: "sites", Resource: desired[0].path, Action: "Create", ParentID: "parent", ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, TaskID: "bound-task", Phase: phase, SubmittedAt: &submitted}
					if strings.HasSuffix(stage, "update") {
						op.Action, op.TargetID = "Update", "target"
					}
					trees := 0
					c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == authPath {
							fmt.Fprint(w, `{"Token":"test"}`)
							return
						}
						if r.Method != "GET" || r.URL.Path != taskPath+"bound-task/tree" {
							t.Error("root fallback or mutation attempted")
							w.WriteHeader(500)
							return
						}
						trees++
						if mode == "unavailable" {
							w.WriteHeader(404)
							return
						}
						if mode == "partial" {
							w.WriteHeader(206)
						}
						root := map[string]any{"id": "bound-task", "startTime": submitted.UnixMilli(), "endTime": now.UnixMilli(), "isError": false}
						child := map[string]any{"id": "child", "parentId": "bound-task", "rootId": "bound-task", "startTime": submitted.UnixMilli(), "endTime": now.UnixMilli(), "isError": false}
						switch mode {
						case "pending-child":
							child["endTime"] = 0
						case "failed-child":
							child["isError"] = true
						case "foreign-child":
							child["rootId"] = "foreign"
						case "missing-root":
							root["id"] = "wrong-root"
						}
						writeDefinitionTaskFixture(w, r, root, child)
					})
					plan := configurationApplyPlan{section: "sites", recoverKnownTask: siteKnownTaskRecovery(desired), observeCompletion: c.siteTaskCompletion(desired)}
					before := op.DeepCopy()
					done, err := plan.taskComplete(context.Background(), c, op, now)
					if mode == "complete" && (!done || err != nil) || mode == "pending-child" && (done || err != nil) || mode != "complete" && mode != "pending-child" && (done || err == nil) {
						t.Fatal("incorrect whole-tree completion result", done, err)
					}
					if trees != 1 || !reflect.DeepEqual(before, op) {
						t.Fatal("observer changed retained operation or repeated request")
					}
				})
			}
		}
	}
}

func TestSiteKnownTaskRecoveryScope(t *testing.T) {
	desired, _ := siteDispatchFixture("area-create")
	now := metav1.Now()
	for _, mode := range []string{"create", "update", "taskless", "undeclared", "bulk", "root", "delete", "prepared", "parentless", "physical", "created-id", "credentials", "foreign-section", "bad-id", "missing-config", "missing-time", "accepted"} {
		t.Run(mode, func(t *testing.T) {
			op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), Section: "sites", Resource: desired[0].path, Action: "Create", ParentID: "parent", ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, TaskID: "bound-task", Phase: "Unknown", SubmittedAt: &now}
			switch mode {
			case "update":
				op.Action, op.TargetID = "Update", "target"
			case "taskless":
				op.TaskID = ""
			case "undeclared":
				op.Resource = "Global/Other"
			case "bulk":
				op.Resource = siteBulkResourcePrefix + "other"
			case "root":
				op.Resource = "Global"
			case "delete":
				op.Action, op.TargetID = "Delete", "target"
			case "prepared":
				op.PreparedAt = &now
			case "parentless":
				op.ParentID = ""
			case "physical":
				op.TargetSerial = "serial"
			case "created-id":
				op.CreatedResourceID = "created"
			case "credentials":
				op.CredentialVersions = []cvk.NetworkControllerConfigurationCredentialVersion{{}}
			case "foreign-section":
				op.Section = "wireless"
			case "bad-id":
				op.ID = "bad"
			case "missing-config":
				op.ConfigUID = ""
			case "missing-time":
				op.SubmittedAt = nil
			case "accepted":
				op.Phase = "Accepted"
			}
			if siteKnownTaskRecovery(desired)(op) != (mode == "create" || mode == "update") {
				t.Fatal("observation permission escaped declared acknowledged singular scope")
			}
		})
	}
}
