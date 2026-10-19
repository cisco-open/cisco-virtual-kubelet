// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Retained identity selects recovery before edited/empty intent can fall through
// to another inventory family. This predicate never grants a new native write.
func retainedPnPRemovalSelected(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	if nc == nil || c == nil || nc.Status.Configuration == nil {
		return false
	}
	state := nc.Status.Configuration
	if state.MetadataHandoff != nil && pnpRemovalSelected(state.MetadataHandoff.Operation.Resource) || state.Operation != nil && pnpRemovalSelected(state.Operation.Resource) {
		return true
	}
	if c.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyDelete {
		return false
	}
	for _, r := range state.ActionApplications {
		if r.Operation.ConfigUID == string(c.UID) && r.Operation.ConfigName == c.Name && (isPnPRegistrationOperation(r.Operation.Section, r.Operation.Resource) || pnpRemovalSelected(r.Operation.Resource)) {
			return true
		}
	}
	return false
}

// Public source pruning uses the same deterministic cohort and journal as the
// private executor. New preparation requires an explicitly configured metadata
// runtime; only the original manager-sealed Prepared operation can dispatch.
// A completed metadata prune does not attest physical onboarding convergence.
func (r *configReconciler) evaluatePnPRemovalRecovery(ctx context.Context, nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, s *configSourceSnapshot, status *configapi.NetworkControllerConfigStatus) (bool, string, string) {
	pending := "PnP cleanup retains ownership and its journal; an installed metadata runtime is required for new dispatch, and full provisioning convergence remains unavailable"
	if nc.Status.Configuration == nil {
		return true, "PnPRemovalRecoveryRequired", "PnP ownership is unavailable; no record can be adopted for cleanup"
	}
	if nc.Status.Configuration.Operation == nil {
		if nc.Status.Configuration.MetadataHandoff != nil {
			return true, "PnPRemovalSettlementPending", "Original metadata custody must be settled and retired by the manager before a new cleanup operation"
		}
		if !r.a.metadataAdmissionReady() {
			return true, "PnPRemovalDispatchUnavailable", pending
		}
		cohort, err := ownedPnPRemovalCohort(nc, c, s)
		if err != nil {
			return true, "PnPRemovalRecoveryRequired", "Complete source and creator-bound cleanup cohort could not be validated; ownership is retained"
		}
		if len(cohort) == 0 {
			return true, "PnPRemovalComplete", "No omitted owned PnP registrations remain; this does not attest site claiming or device provisioning convergence"
		}
		if err := r.a.metadataAdmission.authorize(ctx, nc.DeepCopy()); err != nil {
			return true, "PnPRemovalRuntimeUnavailable", "Current Pod-bound caller and installed metadata execution authority must be verified before preparation"
		}
		write, err := r.a.planOwnedPnPRemoval(ctx, c, s, cohort[0].Baseline.ID)
		if err != nil {
			return true, "PnPRemovalRecoveryRequired", "Cleanup requires an unchanged eligible native cohort and sufficient retained receipt capacity"
		}
		if err := r.a.metadataAdmission.authorize(ctx, nc.DeepCopy()); err != nil {
			return true, "PnPRemovalRuntimeUnavailable", "Metadata execution authority changed during native preflight; no new cleanup journal was prepared"
		}
		if _, err := r.a.prepareConfiguration(ctx, c, s, "inventory", write.resource, write.action, write.identity); err != nil {
			return true, "PnPRemovalRecoveryRequired", "Cleanup preparation was not acknowledged; a fresh journal read is required before proceeding"
		}
		return true, "PnPRemovalHandoffPending", "Owned cleanup is Prepared; manager identity admission and the complete metadata fence cohort are required before dispatch"
	}
	op := nc.Status.Configuration.Operation
	if !validPnPRemovalOperation(op) {
		return true, "PnPRemovalRecoveryRequired", "Retained cleanup has incomplete identity or an unsupported protocol; no native request is replayed"
	}
	if _, _, _, err := r.a.pnpRemovalContext(ctx, c, s, op.TargetID); err != nil {
		return true, "PnPRemovalRecoveryRequired", "Current source, owner or canonical baseline does not authorize cleanup recovery"
	}
	if done, err := r.a.observeOwnedInventoryDeployment(ctx, c, s, op, func(context.Context) (bool, error) { return true, nil }); err != nil || !done {
		return true, "PnPRemovalRecoveryRequired", "Retained operation no longer matches the current source and owner"
	}
	switch op.Phase {
	case "Verifying":
		if err := r.a.recordOwnedPnPRemoval(ctx, c, s, op.ID); err != nil {
			return true, "PnPRemovalRecoveryRequired", "Acknowledged cleanup still requires independent absence and an atomic bound receipt"
		}
		return true, "PnPRemovalVerified", "Acknowledged cleanup absence was independently verified; release remains receipt-gated"
	case "Verified":
		if err := r.a.finishConfiguration(ctx, op.ID); err != nil {
			return true, "PnPRemovalRecoveryRequired", "Verified cleanup lacks intact creator, owner or absence evidence; journal retained"
		}
		return true, "PnPRemovalRecovered", pending
	case "Prepared":
		if !r.a.metadataAdmissionReady() {
			return true, "PnPRemovalDispatchUnavailable", pending
		}
		h := nc.Status.Configuration.MetadataHandoff
		if h == nil || h.ReadyAt == nil {
			return true, "PnPRemovalHandoffPending", "Original Prepared cleanup is waiting for manager sealing and the complete metadata fence cohort"
		}
		if err := r.a.resumeOwnedPnPMetadataRemoval(ctx, c, s, op); err != nil {
			if errors.Is(err, errConfigurationNotDispatched) {
				return true, "PnPRemovalSettlementPending", "Cleanup was certified refused before native dispatch; original custody must settle before replanning"
			}
			return true, "PnPRemovalRecoveryRequired", "Original cleanup could not be acknowledged; retained journal and custody prohibit a replacement submission"
		}
		return true, "PnPRemovalSubmitted", "Original fenced cleanup was acknowledged; independent native absence and its bound receipt are required next"
	default:
		return true, "PnPRemovalRecoveryRequired", "Cleanup acknowledgement is uncertain; native absence cannot authorize replay or completion"
	}
}
