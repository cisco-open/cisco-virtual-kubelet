// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerhandoff

import (
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
)

func TestPreparationCancellationBoundary(t *testing.T) {
	now := metav1.Now()
	up := &ops.IOSXESoftwareUpgrade{ObjectMeta: metav1.ObjectMeta{UID: "parent"}}
	up.Spec.ImageSource.CatalystCenter = &ops.CatalystCenterImageSource{Preparation: &ops.SWIMPreparationPolicyRef{SHA256: "policy"}}
	up.Status.ManagerControl = &ops.UpgradeManagerControlStatus{Cancel: true}
	up.Status.ControllerHandoff = &ops.UpgradeControllerHandoffStatus{Name: "handoff", UID: "journal", Preparation: []ops.SWIMDevicePreparation{{ID: "prep", Stage: "ReadyToDistribute", Phase: "Complete", CompletedAt: &now, PolicySHA256: "policy", Files: []ops.SWIMPreparationFile{{ClaimedAt: &now, RemovedAt: &now}}}}}
	h := &ops.CatalystCenterSWIMHandoff{ObjectMeta: metav1.ObjectMeta{Name: "handoff", UID: "journal"}, Spec: ops.CatalystCenterSWIMHandoffSpec{UpgradeUID: "parent"}, Status: ops.CatalystCenterSWIMHandoffStatus{Phase: "CheckingReadiness", ReadinessFor: "ReadyToDistribute", ReadinessTask: "readiness", ReadinessNotBefore: &now, InventorySyncs: []ops.SWIMInventorySync{{Stage: "ReadyToDistribute", PreparationID: "prep", Task: "sync", CompletedAt: &now}}}}
	if !PreparationCancellationSettled(up, h) {
		t.Fatal("settled first preparation rejected")
	}
	for name, mutate := range map[string]func(*ops.IOSXESoftwareUpgrade, *ops.CatalystCenterSWIMHandoff){
		"not cancelled": func(u *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) {
			u.Status.ManagerControl.Cancel = false
		},
		"distribution claim": func(u *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) {
			h.Status.DistributionClaim = "claim"
		},
		"activation claim": func(u *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) {
			h.Status.ActivationClaim = "claim"
		},
		"sync pending": func(u *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) {
			h.Status.InventorySyncs[0].CompletedAt = nil
		},
		"sync mismatch": func(u *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) {
			h.Status.InventorySyncs[0].PreparationID = "other"
		},
		"file unknown": func(u *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) {
			u.Status.ControllerHandoff.Preparation[0].Files[0].RemovedAt = nil
		},
		"unknown phase": func(u *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) { h.Status.Phase = "OutcomeUnknown" },
		"wrong policy": func(u *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) {
			u.Spec.ImageSource.CatalystCenter.Preparation.SHA256 = "other"
		},
		"wrong stage": func(u *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) {
			h.Status.ReadinessFor = "ReadyToActivate"
		},
		"wrong parent": func(u *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) { h.Spec.UpgradeUID = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			u, j := up.DeepCopy(), h.DeepCopy()
			mutate(u, j)
			if PreparationCancellationSettled(u, j) {
				t.Fatal("unsafe settlement accepted")
			}
		})
	}
}

func TestPausedBeforeActivationPreparation(t *testing.T) {
	now := metav1.Now()
	up := &ops.IOSXESoftwareUpgrade{ObjectMeta: metav1.ObjectMeta{UID: "parent"}}
	up.Spec.ImageSource.CatalystCenter = &ops.CatalystCenterImageSource{Preparation: &ops.SWIMPreparationPolicyRef{SHA256: "policy"}}
	up.Status.Phase = ops.UpgradePhaseStaging
	up.Status.ManagerControl = &ops.UpgradeManagerControlStatus{Pause: true}
	up.Status.ControllerHandoff = &ops.UpgradeControllerHandoffStatus{Name: "handoff", UID: "journal", Preparation: []ops.SWIMDevicePreparation{{ID: "prep", Stage: "ReadyToDistribute", Phase: "Complete", CompletedAt: &now, PolicySHA256: "policy"}}}
	h := &ops.CatalystCenterSWIMHandoff{ObjectMeta: metav1.ObjectMeta{Name: "handoff", UID: "journal"}, Spec: ops.CatalystCenterSWIMHandoffSpec{UpgradeUID: "parent"}, Status: ops.CatalystCenterSWIMHandoffStatus{Phase: "Preparing", ReadinessFor: "ReadyToActivate", DistributionClaim: "claim", DistributionTask: "task", DistributionNotBefore: &now, InventorySyncs: []ops.SWIMInventorySync{{Stage: "ReadyToDistribute", PreparationID: "prep", Task: "sync", CompletedAt: &now}}, ReadinessHistory: []ops.SWIMReadinessReceipt{{Stage: "ReadyToDistribute", Task: "ready"}}}}
	if !PausedBeforePreparation(up, h) {
		t.Fatal("settled distribution boundary rejected")
	}
	for name, mutate := range map[string]func(*ops.IOSXESoftwareUpgrade, *ops.CatalystCenterSWIMHandoff){
		"not paused": func(u *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) {
			u.Status.ManagerControl.Pause = false
		},
		"distribution pending": func(u *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) { h.Status.Phase = "Distributing" },
		"missing receipt":      func(u *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) { h.Status.DistributionTask = "" },
		"activation claimed": func(u *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) {
			h.Status.ActivationClaim = "claim"
		},
		"second cleanup": func(u *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) {
			u.Status.ControllerHandoff.Preparation = append(u.Status.ControllerHandoff.Preparation, ops.SWIMDevicePreparation{Stage: "ReadyToActivate"})
		},
		"sync unresolved": func(u *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) {
			h.Status.InventorySyncs[0].CompletedAt = nil
		},
		"cleanup unresolved": func(u *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) {
			u.Status.ControllerHandoff.Preparation[0].Phase = "Removing"
		},
	} {
		t.Run(name, func(t *testing.T) {
			u, j := up.DeepCopy(), h.DeepCopy()
			mutate(u, j)
			if PausedBeforePreparation(u, j) {
				t.Fatal("unsafe repair boundary accepted")
			}
		})
	}
}
