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

func tagRecreateFixture(t *testing.T) (*cvk.NetworkControllerConfigurationStatus, cvk.NetworkControllerConfigurationActionApplication) {
	t.Helper()
	creator, applied := tagTestReceipt(true), tagTestReceipt(false)
	full := strings.Repeat("b", 64)
	creator.Resource += ":s:" + full
	applied.Resource += ":s:" + full
	key, _, _ := tagOwnedParts(&creator.Operation)
	deletedOp := *applied.Operation.DeepCopy()
	deletedOp.ID = strings.Repeat("e", 32)
	deletedOp.Action = "Delete"
	deletedOp.Resource = tagDeletePrefix + key + ":" + full + ":i"
	deletedOp.TaskID = "delete-first"
	deletedOp.ConfigGeneration = 2
	at := metav1.NewTime(time.Now().Add(-500 * time.Millisecond))
	verified := metav1.NewTime(at.Add(time.Millisecond))
	deletedOp.SubmittedAt = &at
	deleted := cvk.NetworkControllerConfigurationActionApplication{Section: deletedOp.Section, Resource: tagDeletedPrefix + key + ":" + deletedOp.TargetID, Operation: deletedOp, VerifiedAt: &verified}
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator, applied, deleted}}
	child := creator
	child.Operation = *creator.Operation.DeepCopy()
	child.Operation.ID = strings.Repeat("f", 32)
	child.Operation.ConfigGeneration = 3
	child.Operation.TaskID = "create-second"
	child.Operation.Resource = tagRecreatePrefix + strings.TrimPrefix(creator.Operation.Resource, tagOwnedPrefix) + ":" + deletedOp.ID + ":" + featureCohortDigest(state.ActionApplications)
	childAt := metav1.NewTime(time.Now().Add(-100 * time.Millisecond))
	childVerified := metav1.NewTime(childAt.Add(time.Millisecond))
	child.Operation.SubmittedAt = &childAt
	child.VerifiedAt = &childVerified
	child.Resource = strings.Replace(creator.Resource, "22f33fa0-e275-41c9-a50d-d7e5135adb8c", "11f33fa0-e275-41c9-a50d-d7e5135adb8c", 1)
	if err := tagRecreateAuthority(state, &child.Operation); err != nil {
		t.Fatal("valid predecessor refused", err)
	}
	if err := archiveTagCohort(state, &child.Operation); err != nil {
		t.Fatal(err)
	}
	state.ActionApplications = append(state.ActionApplications, child)
	return state, child
}
func TestTagRecreationArchiveIntegrity(t *testing.T) {
	for _, mode := range []string{"valid", "missing-creator", "missing-applied", "missing-delete", "changed-archive", "wrong-parent", "wrong-digest", "missing-link", "reused-uuid", "foreign-owner", "foreign-name", "wrong-section", "early-child", "duplicate-operation", "orphan"} {
		t.Run(mode, func(t *testing.T) {
			state, child := tagRecreateFixture(t)
			current := &state.ActionApplications[3]
			switch mode {
			case "missing-creator":
				state.ActionApplications = state.ActionApplications[1:]
			case "missing-applied":
				state.ActionApplications = append(state.ActionApplications[:1], state.ActionApplications[2:]...)
			case "missing-delete":
				state.ActionApplications = append(state.ActionApplications[:2], state.ActionApplications[3:]...)
			case "changed-archive":
				state.ActionApplications[0].Resource = strings.Replace(state.ActionApplications[0].Resource, ":s:"+strings.Repeat("b", 64), ":s:"+strings.Repeat("a", 64), 1)
			case "wrong-parent":
				parts := strings.Split(current.Operation.Resource, ":")
				parts[len(parts)-2] = strings.Repeat("a", 32)
				current.Operation.Resource = strings.Join(parts, ":")
			case "wrong-digest":
				parts := strings.Split(current.Operation.Resource, ":")
				parts[len(parts)-1] = strings.Repeat("a", 64)
				current.Operation.Resource = strings.Join(parts, ":")
			case "missing-link":
				current.Operation.Resource = tagOwnedCanonical(current.Operation.Resource)
			case "reused-uuid":
				current.Resource = strings.Replace(current.Resource, "11f33fa0-e275-41c9-a50d-d7e5135adb8c", "22f33fa0-e275-41c9-a50d-d7e5135adb8c", 1)
			case "foreign-owner":
				current.Operation.ConfigUID = "foreign"
			case "foreign-name":
				current.Operation.ConfigName = "foreign"
			case "wrong-section":
				current.Section = "templates"
				current.Operation.Section = "templates"
			case "early-child":
				at := metav1.NewTime(time.Now().Add(-2 * time.Second))
				current.Operation.SubmittedAt = &at
			case "duplicate-operation":
				state.ActionApplications[2].Operation.ID = state.ActionApplications[0].Operation.ID
			case "orphan":
				state.ActionApplications = state.ActionApplications[:3]
			}
			cohorts, err := tagRetiredCohorts(state)
			if mode == "valid" {
				if err != nil || len(cohorts) != 1 || !tagOwnedReceiptPresent(state, &child.Operation) {
					t.Fatal("valid complete chain refused", err)
				}
			} else if err == nil || tagOwnedReceiptPresent(state, &child.Operation) {
				t.Fatal("incomplete or unbound archive accepted", mode)
			}
		})
	}
}
func TestTagRecreationPublicCycles(t *testing.T) {
	for _, section := range []string{"inventory", "templates", "combined"} {
		for _, mode := range []string{"recreate", "recreate-twice", "recreate-foreign", "recreate-resurrected", "recreate-reused-id", "recreate-missing-cohort", "recreate-missing-archive", "recreate-tampered-archive"} {
			t.Run(section+"/"+mode, func(t *testing.T) { exerciseTagPrune(t, section, mode, nil) })
		}
	}
}
