// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
	"time"
)

func TestTagDeletedReceiptIntegrity(t *testing.T) {
	for _, mode := range []string{"valid", "legacy-baseline", "wrong-full-state", "wrong-uuid", "foreign-owner", "wrong-source", "missing-task", "reused-task", "missing-time", "future-time", "wrong-result", "duplicate", "changed-operation"} {
		t.Run(mode, func(t *testing.T) {
			creator := tagTestReceipt(true)
			full := strings.Repeat("e", 64)
			creator.Resource += ":s:" + full
			key, _, _ := tagOwnedParts(&creator.Operation)
			at := metav1.NewTime(time.Now().Add(-time.Second))
			verified := metav1.Now()
			op := *creator.Operation.DeepCopy()
			op.ID = strings.Repeat("f", 32)
			op.Action = "Delete"
			op.Resource = tagDeletePrefix + key + ":" + full + ":i"
			op.TargetID = "22f33fa0-e275-41c9-a50d-d7e5135adb8c"
			op.TaskID = "original-delete-task"
			op.SubmittedAt = &at
			deleted := cvk.NetworkControllerConfigurationActionApplication{Section: op.Section, Resource: tagDeletedPrefix + key + ":" + op.TargetID, Operation: op, VerifiedAt: &verified}
			state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator, deleted}}
			row := &state.ActionApplications[1]
			switch mode {
			case "legacy-baseline":
				state.ActionApplications[0].Resource = strings.TrimSuffix(creator.Resource, ":s:"+full)
			case "wrong-full-state":
				row.Operation.Resource = strings.Replace(row.Operation.Resource, full, strings.Repeat("a", 64), 1)
			case "wrong-uuid":
				row.Operation.TargetID = "11f33fa0-e275-41c9-a50d-d7e5135adb8c"
			case "foreign-owner":
				row.Operation.ConfigUID = "other"
			case "wrong-source":
				row.Operation.Resource = strings.TrimSuffix(row.Operation.Resource, ":i") + ":c"
			case "missing-task":
				row.Operation.TaskID = ""
			case "reused-task":
				row.Operation.TaskID = creator.Operation.TaskID
			case "missing-time":
				row.VerifiedAt = nil
			case "future-time":
				v := metav1.NewTime(time.Now().Add(time.Hour))
				row.VerifiedAt = &v
			case "wrong-result":
				row.Resource = tagDeletedPrefix + key + ":foreign"
			case "duplicate":
				state.ActionApplications = append(state.ActionApplications, deleted)
			case "changed-operation":
				row.Operation.ConfigGeneration++
			}
			_, err := tagDeletedRecords(state)
			if mode == "changed-operation" {
				if tagDeletedReceiptPresent(state, &op) {
					t.Fatal("changed receipt authorized release")
				}
				return
			}
			if (err == nil) != (mode == "valid") {
				t.Fatal("tombstone validation mismatch", mode, err)
			}
			if mode == "valid" && !tagDeletedReceiptPresent(state, &op) {
				t.Fatal("verified exact tombstone did not authorize settlement")
			}
		})
	}
}

func TestTagDeletedMandatorySettlement(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "duplicate", "changed-operation"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, c := journalFixture(t)
			creator := tagTestReceipt(true)
			creator.Operation.ConfigUID = string(c.UID)
			creator.Operation.ConfigName = c.Name
			full := strings.Repeat("e", 64)
			creator.Resource += ":s:" + full
			key, _, _ := tagOwnedParts(&creator.Operation)
			op := *creator.Operation.DeepCopy()
			op.ID = strings.Repeat("f", 32)
			op.Resource = tagDeletePrefix + key + ":" + full + ":i"
			op.Action = "Delete"
			op.TargetID = "22f33fa0-e275-41c9-a50d-d7e5135adb8c"
			op.TaskID = "delete-task"
			at := metav1.NewTime(time.Now().Add(-time.Second))
			op.SubmittedAt = &at
			verified := metav1.Now()
			tombstone := cvk.NetworkControllerConfigurationActionApplication{Section: op.Section, Resource: tagDeletedPrefix + key + ":" + op.TargetID, Operation: op, VerifiedAt: &verified}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: op.Section, ConfigUID: string(c.UID), ConfigName: c.Name}}, Operation: op.DeepCopy(), ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator, tombstone}}
			switch mode {
			case "missing":
				nc.Status.Configuration.ActionApplications = nc.Status.Configuration.ActionApplications[:1]
			case "duplicate":
				nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, tombstone)
			case "changed-operation":
				nc.Status.Configuration.ActionApplications[1].Operation.TaskID = "other-task"
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			err = a.finishConfiguration(ctx, op.ID)
			if mode == "valid" && err != nil {
				t.Fatal("exact durable tombstone could not settle", err)
			}
			if mode != "valid" && err == nil {
				t.Fatal("released Delete without its exact original receipt")
			}
		})
	}
}
