// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"reflect"
	"strings"
	"time"
)

// Historical independent completion plus current native equality is idempotency,
// not fresh device CLI attestation. Never adopt an unaddressed unowned interface.
func (a *adapter) observeLANLinkDeletedEffect(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, nc *cvk.NetworkController, want lanLinkIntent, effect *lanLinkEffect, now time.Time) (lanLinkAddObservation, error) {
	var zero lanLinkAddObservation
	if effect == nil || !effect.deleted {
		return zero, errConfigurationOwned
	}
	if err := requireLANLinkSectionOwner(nc, config); err != nil {
		return zero, err
	}
	op := effect.receipt.Operation.DeepCopy()
	if op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) {
		return zero, errConfigurationOwned
	}
	latest, err := latestLANLinkEffect(nc, config, effect.logical, now)
	if err != nil {
		return zero, err
	}
	if latest == nil || !reflect.DeepEqual(latest.receipt, effect.receipt) {
		return zero, errConfigurationStale
	}
	op.Phase = "Verifying"
	native, targets, err := a.lanLinkDeleteCompletionTargets(ctx, config, want, op, now)
	if err != nil {
		return zero, err
	}
	result, err := lanLinkDeleteResultDigest(native)
	if err != nil {
		return zero, err
	}
	if effect.result != result {
		return zero, errConfigurationStale
	}
	// Older ADD receipts do not expose a recoverable port identity separately
	// from their opaque digest. A newer same-device-pair effect might belong to
	// this port under another pool. Hold ambiguous histories rather than let an
	// obsolete DELETE suppress a newer configuration. Only a matching current
	// declaration plus positive independent-port native ownership can disambiguate;
	// absent that evidence, port-specific migration remains unqualified.
	for i := range nc.Status.Configuration.ActionApplications {
		receipt := &nc.Status.Configuration.ActionApplications[i]
		old := &receipt.Operation
		if receipt.Section != "lan_automation" || !strings.HasPrefix(receipt.Resource, "lan-link:") || !((old.TargetID == op.TargetID && old.ParentID == op.ParentID) || (old.TargetID == op.ParentID && old.ParentID == op.TargetID)) {
			continue
		}
		if old.ID == op.ID {
			return zero, errConfigurationOwned
		}
		if receipt.VerifiedAt == nil || !receipt.VerifiedAt.Before(effect.receipt.VerifiedAt) {
			independent, err := a.independentDeclaredLANAdd(ctx, config, source, nc, native.endpoints, receipt, now)
			if err != nil {
				return zero, err
			}
			if !independent {
				return zero, errConfigurationOwned
			}
		}
	}
	return lanLinkAddObservation{owned: true, resource: effect.receipt.Resource, targets: targets}, nil
}

func (a *adapter) observeOwnedLANLinkDelete(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, nc *cvk.NetworkController, want lanLinkIntent, now time.Time) (lanLinkAddObservation, bool, error) {
	var zero lanLinkAddObservation
	if want.action != "DELETE_LINK" || nc.Status.Configuration == nil {
		return zero, false, nil
	}
	// Addressed records must follow the configured-owned DELETE admission path.
	// This allows omitted-pool intent to resolve the currently owned pool rather
	// than treating an older deleted pool's receipt as ownership of current state.
	endpoints, err := a.client.lanLinkEndpointSnapshot(ctx, want)
	if err != nil {
		return zero, false, err
	}
	for _, endpoint := range []lanLinkEndpoint{endpoints.primary, endpoints.secondary} {
		if endpoint.port.IPv4 != "" || endpoint.port.Mask != "" {
			return zero, false, nil
		}
	}
	var selected *lanLinkEffect
	for i := range nc.Status.Configuration.ActionApplications {
		receipt := &nc.Status.Configuration.ActionApplications[i]
		if receipt.Section != "lan_automation" || !strings.HasPrefix(receipt.Resource, "lan-link-delete:") {
			continue
		}
		effect, err := parseLANLinkEffect(receipt, now)
		if err != nil {
			return zero, true, err
		}
		binding, err := parseLANLinkDeleteResource(receipt.Operation.Resource, receipt.Operation.Action)
		if err != nil {
			return zero, true, err
		}
		if !observedLANLinkDeleteDeclaration(want, binding) {
			continue
		}
		if want.pool != "" {
			logical, err := lanLinkAddLogicalDigest(want, endpoints)
			if err != nil {
				return zero, true, err
			}
			if effect.logical != logical {
				continue
			}
		}
		latest, err := latestLANLinkEffect(nc, config, effect.logical, now)
		if err != nil {
			return zero, true, err
		}
		if latest == nil || !reflect.DeepEqual(effect.receipt, latest.receipt) {
			continue
		}
		if selected != nil && selected.receipt.VerifiedAt.Equal(effect.receipt.VerifiedAt) {
			return zero, true, errConfigurationOwned
		}
		if selected == nil || selected.receipt.VerifiedAt.Before(effect.receipt.VerifiedAt) {
			selected = effect
		}
	}
	if selected == nil {
		return zero, false, nil
	}
	result, err := a.observeLANLinkDeletedEffect(ctx, config, source, nc, want, selected, now)
	return result, true, err
}

// A new ADD after DELETE needs a positive deleted-state check and retirement of
// every original delete member before ordinary empty-port/pool admission resumes.
func (a *adapter) validateLANLinkReAdd(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, nc *cvk.NetworkController, want lanLinkIntent, effect *lanLinkEffect, now time.Time) error {
	original := want
	original.action = "DELETE_LINK"
	binding, err := parseLANLinkDeleteResource(effect.receipt.Operation.Resource, effect.receipt.Operation.Action)
	if err != nil {
		return err
	}
	if binding.scope == "" && !exactLANLinkDeleteDeclaration(original, binding) {
		original.pool = ""
	}
	if !observedLANLinkDeleteDeclaration(original, binding) {
		return errConfigurationStale
	}
	if _, err := a.observeLANLinkDeletedEffect(ctx, config, source, nc, original, effect, now); err != nil {
		return err
	}
	return a.requireLANLinkPredecessorRetired(ctx, nc, effect.receipt)
}
