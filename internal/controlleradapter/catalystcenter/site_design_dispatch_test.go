// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSiteDesignNoDispatchScope(t *testing.T) {
	for _, kind := range []string{"image", "ap-create", "ap-update", "ap-observe"} {
		for _, mode := range []string{"valid", "delete", "physical", "task", "created-id", "prepared", "cancelled", "wrong-section", "bad-hash", "bad-parent", "bad-target", "legacy-image"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				at := metav1.Now()
				op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "config", ConfigUID: "config-uid", ConfigGeneration: 1, ControllerGeneration: 1, Section: "sites", Action: "Update", SubmittedAt: &at, TargetID: "floor", Resource: "floor-image-sync:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)}
				if kind != "image" {
					op.Resource = "planned-ap:" + strings.Repeat("a", 64)
					op.ParentID, op.TargetID = "floor", "ap"
					if kind == "ap-create" || kind == "ap-observe" {
						op.Action, op.TargetID = "Create", ""
					}
					if kind == "ap-observe" {
						op.Resource = plannedAPObservePrefix + strings.Repeat("a", 64)
					}
				}
				switch mode {
				case "delete":
					op.Action = "Delete"
				case "physical":
					op.TargetSerial = "device-serial"
				case "task":
					op.TaskID = "task"
				case "created-id":
					op.CreatedResourceID = "created"
				case "prepared":
					op.PreparedAt = &at
				case "cancelled":
					op.CancellationRequestedAt = &at
				case "wrong-section":
					op.Section = "inventory"
				case "bad-hash":
					op.Resource += "x"
				case "bad-parent":
					if kind == "image" {
						op.ParentID = "parent"
					} else {
						op.ParentID = ""
					}
				case "bad-target":
					if kind == "ap-create" || kind == "ap-observe" {
						op.TargetID = "ap"
					} else {
						op.TargetID = ""
					}
				case "legacy-image":
					op.Resource = "floor-image:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)
				}
				if noDispatchOperationSupported(op) != (mode == "valid") {
					t.Fatal("operation escaped exact nonphysical design refusal scope")
				}
			})
		}
	}
}
