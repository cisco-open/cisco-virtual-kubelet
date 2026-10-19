// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerhandoff

import ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"

// PreparationCancellationSettled is deliberately limited to the first readiness
// boundary. A distribution/activation marker, incomplete cleanup, or ambiguous
// inventory synchronization must retain the mutation fence.
func PreparationCancellationSettled(up *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) bool {
	if up == nil || h == nil || up.Status.ManagerControl == nil || !up.Status.ManagerControl.Cancel ||
		up.Status.ControllerHandoff == nil || up.Spec.ImageSource.CatalystCenter == nil || up.Spec.ImageSource.CatalystCenter.Preparation == nil {
		return false
	}
	s, g := h.Status, up.Status.ControllerHandoff
	if g.Name != h.Name || g.UID != string(h.UID) || h.Spec.UpgradeUID != string(up.UID) ||
		(s.Phase != "CheckingReadiness" && s.Phase != "PreparationCancelled") || s.ReadinessFor != "ReadyToDistribute" ||
		s.DistributionClaim != "" || s.DistributionTask != "" || s.DistributionNotBefore != nil ||
		s.ActivationClaim != "" || s.ActivationTask != "" || s.ActivationNotBefore != nil ||
		s.ReadinessTask == "" || s.ReadinessNotBefore == nil || len(g.Preparation) != 1 || len(s.InventorySyncs) != 1 {
		return false
	}
	p, sync := g.Preparation[0], s.InventorySyncs[0]
	if p.ID == "" || p.Stage != "ReadyToDistribute" || p.Phase != "Complete" || p.CompletedAt == nil ||
		p.PolicySHA256 != up.Spec.ImageSource.CatalystCenter.Preparation.SHA256 ||
		sync.Stage != p.Stage || sync.PreparationID != p.ID || sync.Task == "" || sync.CompletedAt == nil {
		return false
	}
	for _, f := range p.Files {
		if f.ClaimedAt == nil || f.RemovedAt == nil {
			return false
		}
	}
	return true
}

// PausedBeforePreparation proves that replacing a worker cannot replay device
// cleanup or a controller submission. The current pause remains mandatory;
// resuming requires a fresh grant through all normal admission gates.
func PausedBeforePreparation(up *ops.IOSXESoftwareUpgrade, h *ops.CatalystCenterSWIMHandoff) bool {
	if up == nil || h == nil || up.Status.ManagerControl == nil || !up.Status.ManagerControl.Pause || up.Status.ManagerControl.Cancel || up.Status.ControllerHandoff == nil || up.Spec.ImageSource.CatalystCenter == nil || up.Spec.ImageSource.CatalystCenter.Preparation == nil {
		return false
	}
	s, g := h.Status, up.Status.ControllerHandoff
	bound := up.Status.Phase == ops.UpgradePhaseStaging && !up.Status.PrimarySupervisorInstallRequested && !up.Status.StandbySupervisorInstallRequested &&
		g.Name == h.Name && g.UID == string(h.UID) && h.Spec.UpgradeUID == string(up.UID) &&
		s.Phase == "Preparing" &&
		s.ReadinessClaim == "" && s.ReadinessTask == "" && s.ReadinessNotBefore == nil &&
		s.ActivationClaim == "" && s.ActivationTask == "" && s.ActivationNotBefore == nil &&
		!up.Status.PrimarySupervisorActivationRequested && !up.Status.StandbySupervisorActivationRequested && !up.Status.RollbackActivationRequested
	if !bound {
		return false
	}
	if s.ReadinessFor == "ReadyToDistribute" {
		return len(g.Preparation) == 0 && len(s.InventorySyncs) == 0 && len(s.ReadinessHistory) == 0 && s.DistributionClaim == "" && s.DistributionTask == "" && s.DistributionNotBefore == nil
	}
	// Preparing/ReadyToActivate is published only after the controller has
	// verified its exact distribution workflow. Preserve that completed journal;
	// replacement cannot replay distribution or a not-yet-claimed second cleanup.
	if s.ReadinessFor != "ReadyToActivate" || s.DistributionClaim == "" || s.DistributionTask == "" || s.DistributionNotBefore == nil || len(g.Preparation) != 1 || len(s.InventorySyncs) != 1 || len(s.ReadinessHistory) != 1 {
		return false
	}
	p, sync, ready := g.Preparation[0], s.InventorySyncs[0], s.ReadinessHistory[0]
	if p.Stage != "ReadyToDistribute" || p.Phase != "Complete" || p.ID == "" || p.CompletedAt == nil || p.PolicySHA256 != up.Spec.ImageSource.CatalystCenter.Preparation.SHA256 || sync.Stage != p.Stage || sync.PreparationID != p.ID || sync.Task == "" || sync.CompletedAt == nil || ready.Stage != p.Stage || ready.Task == "" {
		return false
	}
	for _, f := range p.Files {
		if f.ClaimedAt == nil || f.RemovedAt == nil {
			return false
		}
	}
	return true
}
