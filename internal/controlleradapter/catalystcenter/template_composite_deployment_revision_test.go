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

func TestCompositeDeploymentReceiptSuppression(t *testing.T) {
	for _, policy := range []string{"NEVER", "ALWAYS", "ON_CHANGE"} {
		for _, mode := range []string{"same", "first", "revision", "version", "foreign", "unowned", "duplicate", "unverified", "bad-time", "bad-receipt", "replaced-device", "active", "missing-graph", "duplicate-graph", "bad-resource", "parent-version", "member-added"} {
			t.Run(policy+"/"+mode, func(t *testing.T) {
				now := time.Now()
				submitted, verified := metav1.NewTime(now.Add(-time.Minute)), metav1.NewTime(now.Add(-time.Second))
				desired := &cvk.NetworkControllerConfigurationOperation{Resource: "template-composite-deploy:" + strings.Repeat("a", 64), DependencyVersions: dependencyJournalIdentity().dependencies, Section: "inventory", Action: "Update", ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, TargetID: "device", ParentID: "site", TargetSerial: "serial"}
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
					desired.DependencyVersions.Entries[1].VersionID = "new-member-version"
					desired.DependencyVersions.Entries[1].Version++
					desired.Resource = "template-composite-deploy:" + strings.Repeat("c", 64)
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
				case "missing-graph":
					desired.DependencyVersions = nil
				case "duplicate-graph":
					desired.DependencyVersions.Entries[1] = desired.DependencyVersions.Entries[0]
				case "bad-resource":
					desired.Resource = "template-composite-deploy:not-hex"
				case "parent-version":
					desired.DependencyVersions.Entries[0].VersionID = "new-parent-version"
					desired.DependencyVersions.Entries[0].Version++
				case "member-added":
					desired.DependencyVersions.Entries = append(desired.DependencyVersions.Entries, desired.DependencyVersions.Entries[1])
					desired.DependencyVersions.Entries[2].ID = "second"
					desired.DependencyVersions.Entries[2].VersionID = "second-version"
				case "active":
					state.Operation = old
				}
				done, err := templateDeploymentAlreadyApplied(state, desired, policy, now)
				valid := mode == "same" || mode == "first" || (mode == "version" || mode == "parent-version" || mode == "member-added") || mode == "revision" && policy != "ON_CHANGE"
				expected := mode == "same" || mode == "revision" && policy == "NEVER" || (mode == "version" || mode == "parent-version" || mode == "member-added") && policy != "ON_CHANGE"
				if (err == nil) != valid || done != expected {
					t.Fatal(mode, policy, done, err)
				}
			})
		}
	}
}
