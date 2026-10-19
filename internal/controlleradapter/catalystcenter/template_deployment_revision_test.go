// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
	"time"
)

func TestTemplateDeploymentReceiptSuppression(t *testing.T) {
	for _, policy := range []string{"NEVER", "ALWAYS", "ON_CHANGE"} {
		for _, mode := range []string{"same", "first", "revision", "version", "foreign", "unowned", "duplicate", "unverified", "bad-time", "bad-receipt", "replaced-device", "active"} {
			t.Run(policy+"/"+mode, func(t *testing.T) {
				now := time.Now()
				submitted, verified := metav1.NewTime(now.Add(-time.Minute)), metav1.NewTime(now.Add(-time.Second))
				desired := &cvk.NetworkControllerConfigurationOperation{Resource: "template-deploy:base/version/2/" + strings.Repeat("a", 64), Section: "inventory", Action: "Update", ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, TargetID: "device", ParentID: "site", TargetSerial: "serial"}
				old := desired.DeepCopy()
				old.ID = strings.Repeat("b", 32)
				old.Phase = "Verified"
				old.TaskID = deploymentFixtureID
				old.SubmittedAt = &submitted
				state := &cvk.NetworkControllerConfigurationStatus{}
				state.Owners = append(state.Owners, cvk.NetworkControllerSectionOwner{Section: "inventory", ConfigName: "config", ConfigUID: "owner"})
				state.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Section: "inventory", Resource: old.Resource, Operation: *old, VerifiedAt: &verified}}
				switch mode {
				case "first":
					state.ActionApplications = nil
				case "revision":
					desired.ConfigGeneration++
				case "version":
					desired.Resource = "template-deploy:base/new-version/3/" + strings.Repeat("a", 64)
				case "foreign":
					state.ActionApplications[0].Operation.ConfigUID = "other"
				case "unowned":
					state.Owners = nil
				case "duplicate":
					state.ActionApplications = append(state.ActionApplications, state.ActionApplications[0])
				case "unverified":
					state.ActionApplications[0].Operation.Phase = "Unknown"
				case "bad-time":
					state.ActionApplications[0].VerifiedAt = nil
				case "bad-receipt":
					state.ActionApplications[0].Operation.TaskID = "generic-task"
				case "replaced-device":
					desired.TargetSerial = "replacement"
				case "active":
					state.Operation = old
				}
				done, err := templateDeploymentAlreadyApplied(state, desired, policy, now)
				valid := mode == "same" || mode == "first" || mode == "version" || mode == "revision" && policy != "ON_CHANGE"
				expected := mode == "same" || mode == "revision" && policy == "NEVER" || mode == "version" && policy != "ON_CHANGE"
				if (err == nil) != valid || done != expected {
					t.Fatal(mode, policy, done, err)
				}
			})
		}
	}
}
