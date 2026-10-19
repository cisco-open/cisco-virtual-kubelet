// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"strings"
	"testing"
	"time"
)

func tagTestReceipt(create bool) cvk.NetworkControllerConfigurationActionApplication {
	key := featureIdentityDigest("selected")
	at := metav1.NewTime(time.Now().Add(-4 * time.Second))
	verified := metav1.NewTime(at.Add(time.Second))
	action, kind, prefix, target := "Create", "c", tagCreatedPrefix, ""
	if !create {
		action, kind, prefix, target = "Update", "u", tagAppliedPrefix, "22f33fa0-e275-41c9-a50d-d7e5135adb8c"
		at = metav1.NewTime(time.Now().Add(-time.Second))
		verified = metav1.NewTime(at.Add(time.Millisecond))
	}
	op := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat(kind, 32), Section: "inventory", Resource: tagOwnedPrefix + kind + ":" + key + ":" + strings.Repeat("a", 64) + ":i", Action: action, Phase: "Verified", TaskID: "task", TargetID: target, SubmittedAt: &at, ConfigUID: "owner", ConfigName: "config", ConfigGeneration: 1, ControllerGeneration: 1}
	// Operation IDs are hexadecimal; use a distinct valid Update identity.
	if !create {
		op.ID = strings.Repeat("d", 32)
	}
	return cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: prefix + key + ":22f33fa0-e275-41c9-a50d-d7e5135adb8c:" + strings.Repeat("b", 64), Operation: op, VerifiedAt: &verified}
}
func TestTagOwnedReceiptIntegrity(t *testing.T) {
	for _, mode := range []string{"valid", "no-creator", "duplicate-creator", "duplicate-applied", "foreign-owner", "foreign-name", "foreign-uuid", "wrong-section", "missing-source-mode", "lost-configmap-binding", "missing-task", "missing-time", "future-time", "wrong-result-prefix", "missing-state-digest", "malformed-private", "task-id-collision", "early-update"} {
		t.Run(mode, func(t *testing.T) {
			creator, applied := tagTestReceipt(true), tagTestReceipt(false)
			state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator, applied}}
			r := &state.ActionApplications[1]
			switch mode {
			case "no-creator":
				state.ActionApplications = state.ActionApplications[1:]
			case "duplicate-creator":
				state.ActionApplications = append(state.ActionApplications, creator)
			case "duplicate-applied":
				state.ActionApplications = append(state.ActionApplications, applied)
			case "foreign-owner":
				r.Operation.ConfigUID = "foreign"
			case "foreign-name":
				r.Operation.ConfigName = "foreign"
			case "foreign-uuid":
				r.Operation.TargetID = "11f33fa0-e275-41c9-a50d-d7e5135adb8c"
			case "wrong-section":
				r.Operation.Section = "templates"
				r.Section = "templates"
			case "missing-source-mode":
				r.Operation.Resource = strings.TrimSuffix(r.Operation.Resource, ":i")
			case "lost-configmap-binding":
				r.Operation.Resource = strings.TrimSuffix(r.Operation.Resource, ":i") + ":c"
			case "missing-task":
				r.Operation.TaskID = ""
			case "missing-time":
				r.VerifiedAt = nil
			case "future-time":
				at := metav1.NewTime(time.Now().Add(time.Hour))
				r.VerifiedAt = &at
			case "wrong-result-prefix":
				r.Resource = strings.Replace(r.Resource, tagAppliedPrefix, tagCreatedPrefix, 1)
			case "missing-state-digest":
				r.Resource = strings.TrimSuffix(r.Resource, ":"+strings.Repeat("b", 64))
			case "malformed-private":
				r.Operation.Resource = tagOwnedPrefix + "garbage"
			case "task-id-collision":
				r.Operation.ID = creator.Operation.ID
			case "early-update":
				r.Operation.SubmittedAt = creator.Operation.SubmittedAt.DeepCopy()
			}
			_, err := tagOwnedRecords(state)
			if (err == nil) != (mode == "valid") {
				t.Fatal("receipt integrity mismatch", mode, err)
			}
		})
	}
}
func TestTagOwnedMandatorySettlementReceipt(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "duplicate", "changed-operation"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			r := tagTestReceipt(true)
			r.Operation.ConfigUID = string(config.UID)
			r.Operation.ConfigName = config.Name
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigUID: string(config.UID), ConfigName: config.Name}}, Operation: r.Operation.DeepCopy(), ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{r}}
			switch mode {
			case "missing":
				nc.Status.Configuration.ActionApplications = nil
			case "duplicate":
				nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, r)
			case "changed-operation":
				nc.Status.Configuration.ActionApplications[0].Operation.ConfigGeneration++
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			err = a.finishConfiguration(ctx, r.Operation.ID)
			if mode == "valid" && err != nil {
				t.Fatal("valid durable creator did not settle", err)
			}
			if mode != "valid" && err == nil {
				t.Fatal("released without exact durable creator")
			}
		})
	}
}
func TestTagOwnedCapacityAndImmutableCreator(t *testing.T) {
	creator := tagTestReceipt(true)
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator}}
	before := creator.DeepCopy()
	update := tagTestReceipt(false).Operation
	if err := tagOwnedCapacity(state, &update); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*before, state.ActionApplications[0]) {
		t.Fatal("Update capacity changed creator")
	}
	if err := tagOwnedCapacity(state, &creator.Operation); err == nil {
		t.Fatal("creator replay accepted")
	}
	for len(state.ActionApplications) < 256 {
		state.ActionApplications = append(state.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{})
	}
	if err := tagOwnedCapacity(state, &update); err == nil {
		t.Fatal("Update without receipt capacity permitted")
	}
}
