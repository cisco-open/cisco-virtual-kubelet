// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRMAUnmarkOrigin(t *testing.T) {
	for _, mode := range []string{"owned", "no-owner", "foreign-owner", "missing", "duplicate", "foreign-receipt", "different-record", "different-device", "different-site", "different-scope", "too-old", "too-new", "before-mark-completion", "same-operation", "unknown", "no-submission"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			submitted := metav1.NewTime(now.Add(-time.Minute))
			verified := metav1.NewTime(now.Add(-30 * time.Second))
			cleanup := metav1.NewTime(now.Add(-10 * time.Second))
			scope := strings.Repeat("a", 64)
			config := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: "config", UID: "uid"}}
			mark := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("b", 32), Section: "inventory", Resource: "rma-mark:" + scope, ConfigName: config.Name, ConfigUID: string(config.UID), TargetID: "device", TargetSerial: "serial", ParentID: "site", Action: "Create", Phase: "Verified", TaskID: "mark-task", SubmittedAt: &submitted}
			state := &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: config.Name, ConfigUID: string(config.UID)}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: "inventory", Resource: "rma-mark-record:record", Operation: mark, VerifiedAt: &verified}}}
			op := mark.DeepCopy()
			op.ID, op.Action, op.Phase, op.TaskID, op.SubmittedAt = strings.Repeat("c", 32), "Delete", "Accepted", "cleanup-task", &cleanup
			created := submitted.UnixMilli()
			record := "record"
			switch mode {
			case "no-owner":
				state.Owners = nil
			case "foreign-owner":
				state.Owners[0].ConfigUID = "foreign"
			case "missing":
				state.ActionApplications = nil
			case "duplicate":
				state.ActionApplications = append(state.ActionApplications, state.ActionApplications[0])
			case "foreign-receipt":
				state.ActionApplications[0].Operation.ConfigUID = "foreign"
			case "different-record":
				record = "other"
			case "different-device":
				op.TargetID = "other"
			case "different-site":
				op.ParentID = "other"
			case "different-scope":
				scope = strings.Repeat("d", 64)
			case "too-old":
				created = submitted.Add(-configurationTaskClockSkew - time.Second).UnixMilli()
			case "too-new":
				created = verified.Add(configurationTaskClockSkew + time.Second).UnixMilli()
			case "before-mark-completion":
				op.SubmittedAt = &submitted
			case "same-operation":
				op.ID = mark.ID
			case "unknown":
				op.Phase = "Unknown"
			case "no-submission":
				op.SubmittedAt = nil
			}
			op.Resource = fmt.Sprintf("rma-unmark:%s:%s:%d", scope, record, created)
			if err := ownedRMAUnmarkOrigin(state, config, op, now); (err == nil) != (mode == "owned") {
				t.Fatal("incorrect cleanup authority", err)
			}
			if mode == "owned" {
				receipt, err := ownedRMAMarkReceipt(state, config, mark.Resource, mark.TargetID, mark.TargetSerial, mark.ParentID, now)
				if err != nil || receipt == nil {
					t.Fatal(err)
				}
				receipt.Operation.TaskID = "changed"
				receipt.VerifiedAt.Time = now
				if state.ActionApplications[0].Operation.TaskID != "mark-task" || !state.ActionApplications[0].VerifiedAt.Equal(&verified) {
					t.Fatal("historical evidence alias escaped")
				}
			}
		})
	}
}
