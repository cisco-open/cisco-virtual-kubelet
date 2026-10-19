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

const siteHandoffParentID = "33333333-3333-4333-8333-333333333333"
const siteHandoffReplacementID = "44444444-4444-4444-8444-444444444444"

func siteParentHandoffFixture(t *testing.T) (*cvk.NetworkControllerConfigurationStatus, *cvk.NetworkControllerConfigurationOperation, cvk.NetworkControllerConfigurationActionApplication) {
	t.Helper()
	child := siteDependentFixture(t, siteHandoffParentID, siteOwnedTestUUID, "Global/Parent/Child", "a", "child-create")
	parent := siteDependentFixture(t, "root", siteHandoffParentID, "Global/Parent", "b", "parent-create")
	parent.ActionApplications[1].Operation.ID = strings.Repeat("5", 32)
	parent, op, row := stageSiteRecreationFixture(t, parent, 3, "6", "parent-recreate", siteHandoffReplacementID, time.Now().Add(-40*time.Millisecond))
	parent = publishSiteRecreationFixture(t, parent, op, row)
	parent.ActionApplications = append(parent.ActionApplications, child.ActionApplications...)
	parent.Operation = nil
	return stageSiteRecreationAtPathFixture(t, parent, "Global/Parent/Child", siteHandoffReplacementID, 4, "7", "child-recreate", "55555555-5555-4555-8555-555555555555", time.Now().Add(-20*time.Millisecond))
}

func stageSiteRecreationAtPathFixture(t *testing.T, state *cvk.NetworkControllerConfigurationStatus, path, parentID string, generation int64, id, task, target string, submitted time.Time) (*cvk.NetworkControllerConfigurationStatus, *cvk.NetworkControllerConfigurationOperation, cvk.NetworkControllerConfigurationActionApplication) {
	t.Helper()
	cohorts, err := siteCurrentRetirementCohorts(state)
	if err != nil {
		t.Fatal(err)
	}
	c, found := cohorts[sitePathDigest(path)]
	if !found {
		t.Fatal("fixture missing predecessor")
	}
	base, _ := sitePrivateCreationParts(c.Creator.Operation.Resource)
	resource := siteRecreatePrefix + strings.Join(base, ":") + ":" + c.Retired.Operation.ID + ":" + c.Digest
	if parentID != c.Retired.Operation.ParentID {
		creators, err := siteCreatedRecords(state)
		if err != nil {
			t.Fatal(err)
		}
		parent, found := siteCreatorForPath(creators, parentSitePath(path))
		if !found {
			t.Fatal("fixture missing replacement parent")
		}
		resource = siteRecreateHandoffPrefix + strings.TrimPrefix(resource, siteRecreatePrefix) + ":" + siteReceiptDigest(parent.receipt)
	}
	stamp := metav1.NewTime(submitted)
	op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat(id, 32), Section: "sites", Resource: resource, Action: "Create", Phase: "Verifying", ConfigName: c.Retired.Operation.ConfigName, ConfigUID: c.Retired.Operation.ConfigUID, ConfigGeneration: generation, ControllerGeneration: 1, ParentID: parentID, TaskID: task, SubmittedAt: &stamp}
	state.Operation = op.DeepCopy()
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: siteCreatedResource(verified, c.Kind, target, strings.Repeat("f", 64)), Operation: *verified, VerifiedAt: &stamp}
	return state, op, row
}

func appendSiteRetirementAtPathFixture(t *testing.T, state *cvk.NetworkControllerConfigurationStatus, path string, generation int64, id, task string, submitted time.Time) {
	t.Helper()
	creators, err := siteCreatedRecords(state)
	if err != nil {
		t.Fatal(err)
	}
	creator, found := siteCreatorForPath(creators, path)
	if !found {
		t.Fatal("fixture creator missing")
	}
	base, _ := sitePrivateCreationParts(creator.receipt.Operation.Resource)
	stamp := metav1.NewTime(submitted)
	op := creator.receipt.Operation.DeepCopy()
	op.ID, op.Action, op.Phase, op.TaskID, op.TargetID, op.ConfigGeneration, op.SubmittedAt = strings.Repeat(id, 32), "Delete", "Verified", task, creator.id, generation, &stamp
	op.Resource = siteRetirementPrefix + strings.Join([]string{creator.kind, sitePathDigest(path), "i", base[3], siteReceiptDigest(creator.receipt), strings.Repeat("0", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)}, ":")
	state.ActionApplications = append(state.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: siteRetiredResource(op), Operation: *op, VerifiedAt: &stamp})
	state.Operation = nil
	if _, err := siteRetiredRecords(state); err != nil {
		t.Fatal("fixture retirement rejected", err)
	}
}

func TestSiteRecreationParentHandoff(t *testing.T) {
	for _, mode := range []string{"complete", "wrong-parent", "same-parent", "missing-parent-archive", "wrong-reference", "v1-reparent", "unknown-version", "borrow-parent-task", "before-parent", "retired-parent", "drop-parent-history"} {
		t.Run(mode, func(t *testing.T) {
			state, op, row := siteParentHandoffFixture(t)
			switch mode {
			case "wrong-parent":
				op.ParentID = "66666666-6666-4666-8666-666666666666"
			case "same-parent":
				op.ParentID = siteHandoffParentID
			case "missing-parent-archive":
				state.ActionApplications = state.ActionApplications[2:]
			case "wrong-reference":
				parts, _ := siteRecreationParts(op.Resource)
				parts[6] = strings.Repeat("0", 64)
				op.Resource = siteRecreateHandoffPrefix + strings.Join(parts, ":")
			case "v1-reparent":
				parts, _ := siteRecreationParts(op.Resource)
				op.Resource = siteRecreatePrefix + strings.Join(parts[:6], ":")
			case "unknown-version":
				op.Resource = strings.Replace(op.Resource, ":v2:", ":v3:", 1)
			case "borrow-parent-task":
				op.TaskID = "parent-recreate"
			case "before-parent":
				stamp := metav1.NewTime(time.Now().Add(-60 * time.Millisecond))
				op.SubmittedAt = &stamp
			case "retired-parent":
				appendSiteRetirementAtPathFixture(t, state, "Global/Parent", 4, "8", "parent-retire-again", time.Now().Add(-10*time.Millisecond))
			}
			state.Operation = op.DeepCopy()
			row.Operation = *op.DeepCopy()
			row.Operation.Phase = "Verified"
			before := state.DeepCopy()
			complete, err := siteCreationPublication(state, op, row)
			valid := mode == "complete" || mode == "drop-parent-history"
			if (err == nil) != valid {
				t.Fatal("parent handoff publication mismatch", mode, err)
			}
			if !reflect.DeepEqual(before, state) {
				t.Fatal("handoff mutated predecessor before publication")
			}
			if !valid {
				return
			}
			if len(complete.ActionApplications) != 6 {
				t.Fatal("combined parent and child history lost")
			}
			if mode == "drop-parent-history" {
				complete.ActionApplications = complete.ActionApplications[2:]
				if _, err := siteCreatedRecords(complete); err == nil {
					t.Fatal("lost parent chain accepted")
				}
			}
		})
	}
}

func TestSiteRecreationParentHandoffArchivedReference(t *testing.T) {
	state, op, row := siteParentHandoffFixture(t)
	complete, err := siteCreationPublication(state, op, row)
	if err != nil {
		t.Fatal(err)
	}
	appendSiteRetirementAtPathFixture(t, complete, "Global/Parent/Child", 5, "8", "child-retire-again", time.Now().Add(-10*time.Millisecond))
	appendSiteRetirementAtPathFixture(t, complete, "Global/Parent", 6, "9", "parent-retire-again", time.Now().Add(-5*time.Millisecond))
	complete, next, parent := stageSiteRecreationAtPathFixture(t, complete, "Global/Parent", "root", 7, "e", "parent-third-create", "66666666-6666-4666-8666-666666666666", time.Now())
	complete, err = siteCreationPublication(complete, next, parent)
	if err != nil {
		t.Fatal("archived referenced parent rejected", err)
	}
	archives, err := siteHistoryCohorts(complete)
	if err != nil || len(archives) != 3 {
		t.Fatal("historical child handoff lost its referenced parent cohort", err)
	}
}
