// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"reflect"
	"slices"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Pure projection of the original pre-submission ledger. It accepts only the
// acknowledged/verified original absence receipt or its certified non-dispatch.
// Native absence, a stale Source model or Unknown journal state cannot mint it.
// The manager independently binds this projection to the original fence worker.
func pnpMetadataSettlementState(nc *cvk.NetworkController) (*cvk.NetworkControllerConfigurationStatus, error) {
	if nc == nil || nc.Spec.Type != TypeName || nc.Status.Configuration == nil {
		return nil, errConfigurationOwned
	}
	state := nc.Status.Configuration
	h := state.MetadataHandoff
	if h == nil || h.Protocol != cvk.NetworkControllerMetadataHandoffProtocol || !validPnPRemovalOperation(&h.Operation) || h.Operation.Phase != "Prepared" || h.Operation.ControllerGeneration < 1 || h.Operation.ControllerGeneration > nc.Generation || h.ReadyAt == nil || h.ReadyAt.IsZero() || h.ReadyAt.After(time.Now()) || len(h.Fences) == 0 || len(h.Fences) != len(h.PhysicalIdentities) {
		return nil, errConfigurationOwned
	}
	original := state.DeepCopy()
	original.MetadataHandoff = nil
	original.Operation = h.Operation.DeepCopy()
	if h.CancelledAt != nil {
		if state.Operation != nil || h.CancelledAt.IsZero() || h.CancelledAt.Before(h.ReadyAt) || h.CancelledAt.After(time.Now()) || h.RefusedAt != nil || h.RefusedSubmissionAt != nil {
			return nil, errConfigurationStale
		}
		for _, r := range state.ActionApplications {
			if r.Operation.ID == h.Operation.ID || r.Resource == pnpRemovedKey(h.Operation.Resource) {
				return nil, errConfigurationOwned
			}
		}
	} else if h.RefusedAt != nil {
		if state.Operation != nil || h.RefusedAt.IsZero() || h.RefusedSubmissionAt == nil || h.RefusedSubmissionAt.IsZero() || h.RefusedSubmissionAt.Before(h.ReadyAt) || h.RefusedAt.Before(h.RefusedSubmissionAt) || h.RefusedAt.After(time.Now()) {
			return nil, errConfigurationStale
		}
		for _, r := range state.ActionApplications {
			if r.Operation.ID == h.Operation.ID || r.Resource == pnpRemovedKey(h.Operation.Resource) {
				return nil, errConfigurationOwned
			}
		}
	} else {
		op := state.Operation
		if h.RefusedSubmissionAt != nil || !validPnPRemovalOperation(op) || op.Phase != "Verified" || !pnpRemovedReceiptPresent(state, op) || op.SubmittedAt.Before(h.ReadyAt) {
			return nil, errConfigurationOwned
		}
		prepared := op.DeepCopy()
		prepared.Phase = "Prepared"
		prepared.SubmittedAt = nil
		if !reflect.DeepEqual(*prepared, h.Operation) {
			return nil, errConfigurationStale
		}
		original.ActionApplications = nil
		for _, r := range state.ActionApplications {
			if r.Operation.ID != op.ID {
				original.ActionApplications = append(original.ActionApplications, *r.DeepCopy())
			}
		}
	}
	return pnpMetadataOriginalLedger(nc, original)
}

func pnpMetadataCancellationState(nc *cvk.NetworkController) (*cvk.NetworkControllerConfigurationStatus, error) {
	if nc == nil || nc.Spec.Type != TypeName || nc.Status.Configuration == nil {
		return nil, errConfigurationOwned
	}
	state := nc.Status.Configuration
	h := state.MetadataHandoff
	if h == nil || h.Protocol != cvk.NetworkControllerMetadataHandoffProtocol || !validPnPRemovalOperation(&h.Operation) || h.Operation.Phase != "Prepared" || h.Operation.ControllerGeneration > nc.Generation || h.ReadyAt == nil || h.ReadyAt.IsZero() || h.ReadyAt.After(time.Now()) || len(h.Fences) == 0 || len(h.Fences) != len(h.PhysicalIdentities) || h.CancelledAt != nil || h.RefusedAt != nil || h.RefusedSubmissionAt != nil || h.SettlementDigest != "" || h.ReleasedAt != nil || state.Operation == nil || !reflect.DeepEqual(*state.Operation, h.Operation) {
		return nil, errConfigurationOwned
	}
	for _, r := range state.ActionApplications {
		if r.Operation.ID == h.Operation.ID || r.Resource == pnpRemovedKey(h.Operation.Resource) {
			return nil, errConfigurationOwned
		}
	}
	original := state.DeepCopy()
	original.MetadataHandoff = nil
	return pnpMetadataOriginalLedger(nc, original)
}

func pnpMetadataOriginalLedger(nc *cvk.NetworkController, original *cvk.NetworkControllerConfigurationStatus) (*cvk.NetworkControllerConfigurationStatus, error) {
	h := nc.Status.Configuration.MetadataHandoff
	if err := pnpRemovalCapacity(original, &h.Operation); err != nil {
		return nil, err
	}
	_, selected, err := pnpRemovalCreator(original, h.Operation.ConfigUID, h.Operation.ConfigName, h.Operation.TargetID, h.Operation.ControllerGeneration)
	if err != nil || !slices.Contains(h.PhysicalIdentities, selected.Serial) {
		return nil, errConfigurationOwned
	}
	// Every retained serial must have its unique original canonical cleanup root.
	// The sealed binding, checked by the manager, preserves the exact original
	// omitted subset without reconstructing desired intent from a changed model.
	serials := map[string]bool{}
	for _, serial := range h.PhysicalIdentities {
		if serials[serial] {
			return nil, errConfigurationOwned
		}
		serials[serial] = true
	}
	matched := map[string]bool{}
	for _, r := range original.ActionApplications {
		if !isPnPRegistrationOperation(r.Operation.Section, r.Operation.Resource) {
			continue
		}
		creator, b, err := pnpRemovalCreator(original, h.Operation.ConfigUID, h.Operation.ConfigName, r.Operation.CreatedResourceID, h.Operation.ControllerGeneration)
		if err != nil {
			return nil, err
		}
		if !serials[b.Serial] {
			continue
		}
		if matched[b.Serial] || creator.Operation.ConfigGeneration > h.Operation.ConfigGeneration {
			return nil, errConfigurationOwned
		}
		matched[b.Serial] = true
	}
	if len(matched) != len(serials) {
		return nil, errConfigurationOwned
	}
	return original, nil
}
