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

func TestSiteNoDispatchScope(t *testing.T) {
	for _, mode := range []string{"create", "update", "delete", "root", "bulk", "image", "parent-missing", "self-parent", "create-target", "update-missing-target", "serial", "created-id", "prepared", "task", "canceled", "section", "operation-id"} {
		t.Run(mode, func(t *testing.T) {
			now := metav1.NewTime(time.Now())
			op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), Section: "sites", Resource: "Global/Lab", Action: "Create", ConfigName: "sites", ConfigUID: "config", ConfigGeneration: 1, ControllerGeneration: 1, ParentID: "root", Phase: "Submitting", SubmittedAt: &now}
			switch mode {
			case "update":
				op.Action, op.TargetID = "Update", "area"
			case "delete":
				op.Action, op.TargetID = "Delete", "area"
			case "root":
				op.Resource = "Global"
			case "bulk":
				op.Resource = "site-bulk-create:other"
			case "image":
				op.Resource = "floor-image-sync:other"
			case "parent-missing":
				op.ParentID = ""
			case "self-parent":
				op.Action, op.TargetID = "Update", "root"
			case "create-target":
				op.TargetID = "area"
			case "update-missing-target":
				op.Action = "Update"
			case "serial":
				op.TargetSerial = "device"
			case "created-id":
				op.CreatedResourceID = "created"
			case "prepared":
				op.PreparedAt = &now
			case "task":
				op.TaskID = "task"
			case "canceled":
				op.CancellationRequestedAt = &now
			case "section":
				op.Section = "inventory"
			case "operation-id":
				op.ID = "invalid"
			}
			if noDispatchOperationSupported(op) != (mode == "create" || mode == "update") {
				t.Fatal("site no-dispatch authority escaped singular metadata scope")
			}
		})
	}
}
