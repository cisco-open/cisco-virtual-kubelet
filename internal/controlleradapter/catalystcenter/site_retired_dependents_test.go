// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"reflect"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func siteDependentFixture(t *testing.T, parentID, childID, childPath, operationID, task string) *cvk.NetworkControllerConfigurationStatus {
	t.Helper()
	creator, _ := siteAppliedTestRecords()
	creator.Operation.ParentID = parentID
	creator.Operation.ID = strings.Repeat(operationID, 32)
	creator.Operation.TaskID = task
	creator.Operation.Resource = sitePrivateCreatePrefix + "area:" + sitePathDigest(childPath) + ":i:" + strings.Repeat("a", 64)
	creator.Resource = siteCreatedResource(&creator.Operation, "area", childID, strings.Repeat("b", 64))
	state := &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "sites", ConfigName: "config", ConfigUID: "owner"}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator}}
	appendSiteRetirementFixture(t, state, 2, "d", task+"-retire", time.Now().Add(-80*time.Millisecond))
	return state
}

func retirementDependentTestRows(t *testing.T, owners []cvk.NetworkControllerSectionOwner, template cvk.NetworkControllerConfigurationActionApplication, parentID, id, path, createID, retireID string, generation int64, age time.Duration) []cvk.NetworkControllerConfigurationActionApplication {
	t.Helper()
	creator := *template.DeepCopy()
	parts, _ := sitePrivateCreationParts(creator.Operation.Resource)
	creator.Operation.ParentID, creator.Operation.ID, creator.Operation.TaskID = parentID, strings.Repeat(createID, 32), "child-create-"+createID
	creator.Operation.ControllerGeneration = 1
	creator.Operation.Resource = sitePrivateCreatePrefix + strings.Join([]string{"area", sitePathDigest(path), parts[2], parts[3]}, ":")
	creator.Resource = siteCreatedResource(&creator.Operation, "area", id, strings.Repeat("b", 64))
	state := &cvk.NetworkControllerConfigurationStatus{Owners: owners, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator}}
	appendSiteRetirementFixture(t, state, generation, retireID, "child-retire-"+retireID, time.Now().Add(age))
	return state.ActionApplications
}

func TestSiteRetiredDependentCohorts(t *testing.T) {
	const parentID = "33333333-3333-4333-8333-333333333333"
	for _, mode := range []string{"complete", "unretired", "pending", "missing-creator", "wrong-owner", "wrong-key", "newer-generation", "newer-controller", "after-parent", "borrowed-task", "duplicate", "effect", "credential", "native-uuid", "native-path", "grandchild", "live-grandchild", "grandchild-uuid", "grandchild-after-child", "grandchild-borrowed-task", "archived", "archive-edited"} {
		t.Run(mode, func(t *testing.T) {
			state := siteDependentFixture(t, parentID, siteOwnedTestUUID, "Global/Parent/Child", "a", "child-create")
			stamp := metav1.NewTime(time.Now())
			parent := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("9", 32), ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 5, ControllerGeneration: 1, TargetID: parentID, SubmittedAt: &stamp, TaskID: "parent-task"}
			actual := []Site{}
			switch mode {
			case "unretired":
				state.ActionApplications = state.ActionApplications[:1]
			case "pending":
				state.ActionApplications[1].Operation.Phase = "Accepted"
			case "missing-creator":
				state.ActionApplications = state.ActionApplications[1:]
			case "wrong-owner":
				parent.ConfigUID = "foreign"
			case "wrong-key":
				// Exercise the authority scope without weakening the child proof.
			case "newer-generation":
				parent.ConfigGeneration = 1
			case "newer-controller":
				state.ActionApplications[1].Operation.ControllerGeneration = 2
			case "after-parent":
				parent.SubmittedAt = state.ActionApplications[0].VerifiedAt.DeepCopy()
			case "borrowed-task":
				parent.TaskID = state.ActionApplications[1].Operation.TaskID
			case "duplicate":
				state.ActionApplications = append(state.ActionApplications, state.ActionApplications[1])
			case "effect":
				state.ActionApplications[1].EffectReceipts = []string{"unretired-effect"}
			case "credential":
				state.CredentialApplications = []cvk.NetworkControllerConfigurationCredentialApplication{{TargetID: siteOwnedTestUUID}}
			case "native-uuid":
				actual = []Site{{ID: siteOwnedTestUUID, NameHierarchy: "Global/Moved"}}
			case "native-path":
				actual = []Site{{ID: "replacement", NameHierarchy: "Global/Parent/Child"}}
			case "grandchild", "live-grandchild", "grandchild-uuid", "grandchild-after-child", "grandchild-borrowed-task":
				grand := siteDependentFixture(t, siteOwnedTestUUID, "22222222-2222-4222-8222-222222222222", "Global/Parent/Child/Grandchild", "e", "grandchild-create")
				grand.ActionApplications[1].Operation.ID = strings.Repeat("f", 32)
				stamp := metav1.NewTime(time.Now().Add(-200 * time.Millisecond))
				grand.ActionApplications[1].Operation.SubmittedAt, grand.ActionApplications[1].VerifiedAt = &stamp, &stamp
				if mode == "grandchild-after-child" {
					now := metav1.Now()
					grand.ActionApplications[1].Operation.SubmittedAt, grand.ActionApplications[1].VerifiedAt = &now, &now
				}
				if mode == "grandchild-borrowed-task" {
					grand.ActionApplications[1].Operation.TaskID = state.ActionApplications[1].Operation.TaskID
				}
				if mode == "live-grandchild" {
					grand.ActionApplications = grand.ActionApplications[:1]
				}
				state.ActionApplications = append(state.ActionApplications, grand.ActionApplications...)
				if mode == "grandchild-uuid" {
					actual = []Site{{ID: "22222222-2222-4222-8222-222222222222", NameHierarchy: "Global/Moved"}}
				}
			case "archived", "archive-edited":
				state, op, row := stageSiteRecreationFixture(t, state, 3, "e", "child-recreate", "22222222-2222-4222-8222-222222222222", time.Now().Add(-40*time.Millisecond))
				working := publishSiteRecreationFixture(t, state, op, row)
				appendSiteRetirementFixture(t, working, 4, "f", "child-re-retire", time.Now().Add(-20*time.Millisecond))
				state.ActionApplications = working.ActionApplications
				if mode == "archive-edited" {
					state.ActionApplications[0].Operation.TaskID = "edited-archived-task"
				}
			}
			key := strings.Repeat("a", 64)
			if mode == "wrong-key" {
				key = strings.Repeat("b", 64)
			}
			before := state.DeepCopy()
			d, err := siteRetiredDependentCohorts(state, parent, "area", key)
			valid := mode == "complete" || mode == "grandchild" || mode == "archived" || mode == "native-uuid" || mode == "native-path" || mode == "grandchild-uuid"
			if (err == nil) != valid {
				t.Fatal("descendant authority mismatch", mode, err)
			}
			if !reflect.DeepEqual(before, state) {
				t.Fatal("descendant validation changed retained history")
			}
			if err != nil {
				return
			}
			err = d.checkAbsent(actual)
			if (err == nil) != (mode == "complete" || mode == "grandchild" || mode == "archived") {
				t.Fatal("reappeared retired dependency accepted", err)
			}
			if mode == "grandchild" && len(d.ids) != 2 {
				t.Fatal("grandchild omitted from dependency closure")
			}
			if mode == "archived" && (len(d.ids) != 2 || len(d.rows) != 4) {
				t.Fatal("archived child lifecycle lost")
			}
		})
	}
}
