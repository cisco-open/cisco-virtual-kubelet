// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const plannedAPIdentityTestUUID = "22222222-2222-4222-8222-222222222222"

func plannedAPIdentityTestRecord() cvk.NetworkControllerConfigurationActionApplication {
	at := metav1.NewTime(time.Now().Add(-time.Second))
	op := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), Section: "sites", Resource: plannedAPObservePrefix + strings.Repeat("b", 64), Action: "Create", Phase: "Verified", ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, ParentID: siteOwnedTestUUID, TaskID: "acknowledged-task", SubmittedAt: &at}
	return cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: plannedAPIdentityResource(&op, plannedAPIdentityTestUUID), Operation: op, VerifiedAt: &at}
}
func TestPlannedAPIdentityReceiptBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "configmap", "source-pruned", "result-pruned", "task-pruned", "no-time", "no-verified-time", "future", "before-submission", "wrong-section", "wrong-floor", "bad-UUID", "wrong-resource", "update", "physical", "prepared", "duplicate", "alias", "same-UUID", "generation", "missing-owner"} {
		t.Run(mode, func(t *testing.T) {
			r := plannedAPIdentityTestRecord()
			switch mode {
			case "configmap", "source-pruned":
				r.Operation.SourceUID, r.Operation.SourceVersion = "source", "revision"
				r.Resource = plannedAPIdentityResource(&r.Operation, plannedAPIdentityTestUUID)
				if mode == "source-pruned" {
					r.Operation.SourceUID, r.Operation.SourceVersion = "", ""
				}
			case "result-pruned":
				r.Resource = ""
			case "task-pruned":
				r.Operation.TaskID = ""
			case "no-time":
				r.Operation.SubmittedAt = nil
			case "no-verified-time":
				r.VerifiedAt = nil
			case "future":
				v := metav1.NewTime(time.Now().Add(time.Hour))
				r.VerifiedAt = &v
			case "before-submission":
				v := metav1.NewTime(time.Now().Add(-time.Hour))
				r.VerifiedAt = &v
			case "wrong-section":
				r.Section = "inventory"
			case "wrong-floor":
				r.Operation.ParentID = "not-a-UUID"
			case "bad-UUID":
				r.Resource = strings.Replace(r.Resource, plannedAPIdentityTestUUID, "bad", 1)
			case "wrong-resource":
				r.Operation.Resource = plannedAPObservePrefix + strings.Repeat("c", 64)
			case "update":
				r.Operation.Action = "Update"
			case "physical":
				r.Operation.TargetSerial = "serial"
			case "prepared":
				r.Operation.PreparedAt = r.Operation.SubmittedAt.DeepCopy()
			case "generation":
				r.Operation.ConfigGeneration = 0
			case "missing-owner":
				r.Operation.ConfigUID = ""
			}
			state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{r}}
			switch mode {
			case "duplicate":
				state.ActionApplications = append(state.ActionApplications, r)
			case "alias":
				other := r
				other.Resource = "unrelated"
				state.ActionApplications = append(state.ActionApplications, other)
			case "same-UUID":
				other := r
				other.Operation.ID = strings.Repeat("d", 32)
				other.Operation.Resource = plannedAPObservePrefix + strings.Repeat("e", 64)
				other.Resource = plannedAPIdentityResource(&other.Operation, plannedAPIdentityTestUUID)
				state.ActionApplications = append(state.ActionApplications, other)
			}
			records, err := plannedAPIdentityRecords(state)
			valid := mode == "valid" || mode == "configmap"
			if (err == nil) != valid || plannedAPIdentityReceiptPresent(state, &r.Operation) != valid {
				t.Fatal("invalid observed identity receipt accepted", err)
			}
			if valid {
				r.Operation.SubmittedAt.Time = time.Time{}
				if records[plannedAPResourceKey(r.Operation.Resource)].receipt.Operation.SubmittedAt.IsZero() {
					t.Fatal("receipt aliases mutable status")
				}
			}
		})
	}
}
func TestPlannedAPIdentityCapacityAndSettlement(t *testing.T) {
	r := plannedAPIdentityTestRecord()
	op := r.Operation.DeepCopy()
	op.Phase = "Prepared"
	op.TaskID = ""
	op.SubmittedAt = nil
	state := &cvk.NetworkControllerConfigurationStatus{}
	if err := plannedAPIdentityCapacity(state, op); err != nil {
		t.Fatal(err)
	}
	state.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{r}
	if err := plannedAPIdentityCapacity(state, op); err == nil {
		t.Fatal("second identity observation admitted for anchored AP")
	}
	partial := r.Operation.DeepCopy()
	partial.Resource = "pruned-resource"
	if !plannedAPIdentitySettlementRequired(state, partial) || plannedAPIdentityReceiptPresent(state, partial) {
		t.Fatal("pruned current operation bypassed retained observation proof")
	}
	state.ActionApplications = nil
	for i := 0; i < 256; i++ {
		state.ActionApplications = append(state.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "other", Resource: "retained"})
	}
	if err := plannedAPIdentityCapacity(state, op); err == nil {
		t.Fatal("count capacity not reserved before dispatch")
	}
	state.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Section: "other", Resource: strings.Repeat("x", 256<<10)}}
	if err := plannedAPIdentityCapacity(state, op); err == nil {
		t.Fatal("byte capacity not reserved before dispatch")
	}
}

func TestPlannedAPObservedFloorIdentityFence(t *testing.T) {
	for _, mode := range []string{"same", "replaced", "missing", "wrong-kind", "foreign-owner", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			_, desired, err := parseSitesWithPlannedAPs([]byte(plannedAPFixture))
			if err != nil {
				t.Fatal(err)
			}
			record := plannedAPIdentityTestRecord()
			record.Operation.Resource = plannedAPObservePrefix + strings.TrimPrefix(plannedAPResource(desired[0]), "planned-ap:")
			record.Resource = plannedAPIdentityResource(&record.Operation, plannedAPIdentityTestUUID)
			records, err := plannedAPIdentityRecords(&cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{record}})
			if err != nil {
				t.Fatal(err)
			}
			e := &plannedAPExecutor{desired: desired, identities: records, identityOwner: "owner"}
			floor := plannedAPTestFloor(siteOwnedTestUUID, desired[0].floor)
			actual := []Site{floor}
			switch mode {
			case "replaced":
				actual[0].ID = plannedAPIdentityTestUUID
			case "missing":
				actual = nil
			case "wrong-kind":
				actual[0].Type = "area"
			case "foreign-owner":
				e.identityOwner = "foreign"
			case "legacy":
				e.identities = nil
			}
			if (e.checkFloorIdentities(actual) == nil) != (mode == "same" || mode == "legacy") {
				t.Fatal("observed floor identity was bypassed")
			}
		})
	}
}
