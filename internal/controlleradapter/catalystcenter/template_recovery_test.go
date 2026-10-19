// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTemplateKnownTaskRecovery(t *testing.T) {
	for _, stage := range templateNoDispatchStages {
		for _, mode := range templateRecoveryModes {
			t.Run(stage+"/"+mode, func(t *testing.T) { exerciseTemplateNoDispatchReplan(t, stage, "recovery-"+mode, nil) })
		}
	}
}

var templateRecoveryModes = []string{"known", "known-legacy", "missing-task", "source-changed", "owner-changed", "parent-changed", "missing-native", "failed-child", "pending-child", "foreign-child", "missing-tree"}

func TestTemplateRecoveryScope(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		raw := templateModel
		prefix := ""
		if grouped {
			raw = strings.Replace(raw, "  templates:\n", "  templates:\n    tags: [{name: scope}]\n", 1)
			prefix = "objects/"
		}
		plan, err := (&client{}).compileConfigurationApply([]byte(raw), []string{"templates"}, map[string]string{"base.j2": "first\n"})
		if err != nil || plan.recoverKnownTask == nil || plan.observeCompletion == nil {
			t.Fatal("missing metadata recovery", err)
		}
		for _, kind := range []string{"project-create", "project-update", "draft-create", "draft-update", "commit"} {
			now := metav1.NewTime(time.Now().Add(-time.Second))
			original := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, Section: "templates", Resource: prefix + "Lab", Action: "Create", Phase: "Unknown", TaskID: "task", SubmittedAt: &now}
			if strings.Contains(kind, "draft") || kind == "commit" {
				original.Resource = prefix + "draft:Lab#base"
				original.ParentID = "project"
			}
			if strings.HasSuffix(kind, "update") || kind == "commit" {
				original.Action = "Update"
				original.TargetID = "base"
			}
			if kind == "commit" {
				original.Resource = prefix + "commit:Lab#base"
			}
			for _, mode := range []string{"known", "taskless", "phase", "section", "unselected", "prefix", "parent", "created", "serial", "group", "dependencies", "cancellation", "prepared", "missing-time", "id", "owner", "generation", "delete", "wrong-action"} {
				t.Run(fmt.Sprint(grouped)+"/"+kind+"/"+mode, func(t *testing.T) {
					op := original.DeepCopy()
					switch mode {
					case "taskless":
						op.TaskID = ""
					case "phase":
						op.Phase = "Accepted"
					case "section":
						op.Section = "inventory"
					case "unselected":
						op.Resource = prefix + "Other"
					case "prefix":
						if grouped {
							op.Resource = strings.TrimPrefix(op.Resource, "objects/")
						} else {
							op.Resource = "objects/" + op.Resource
						}
					case "parent":
						if op.ParentID == "" {
							op.ParentID = "project"
						} else {
							op.ParentID = ""
						}
					case "created":
						op.CreatedResourceID = "created"
					case "serial":
						op.TargetSerial = "serial"
					case "group":
						op.PhysicalGroup = &cvk.NetworkControllerConfigurationGroupIdentity{}
					case "dependencies":
						op.DependencyVersions = &cvk.NetworkControllerConfigurationDependencyVersions{}
					case "cancellation":
						op.CancellationRequestedAt = &now
					case "prepared":
						op.PreparedAt = &now
					case "missing-time":
						op.SubmittedAt = nil
					case "id":
						op.ID = "malformed"
					case "owner":
						op.ConfigUID = ""
					case "generation":
						op.ControllerGeneration = 0
					case "delete":
						op.Action = "Delete"
					case "wrong-action":
						if op.Action == "Create" {
							op.TargetID = "unexpected"
						} else {
							op.TargetID = ""
						}
					}
					if got := plan.canObserveUnknownTask(op); got != (mode == "known") {
						t.Fatalf("recovery=%t", got)
					}
				})
			}
		}
	}
}
