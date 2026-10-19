// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"fmt"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Plan the registration stage using retained ownership before considering a
// create. The shared journal remains responsible for submission and recovery;
// this stage does not discard or execute the manifest's later claim intent.
func (a *adapter) planPnPRegistrationStage(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want pnpDeviceIntent) (*configurationWrite, string, error) {
	if a == nil || !supportedPnPRegistrationConfig(config) {
		return nil, "", errConfigurationTransition
	}
	id, err := a.resolveOwnedPnPRegistration(ctx, config, source, want)
	if err != nil || id != "" {
		return nil, id, err
	}
	var raw []byte
	if source != nil {
		raw = source.raw
	} else if config.Spec.Source.Inline != nil {
		raw = config.Spec.Source.Inline.Raw
	}
	wants, err := parsePnPInventory(raw)
	if err != nil {
		return nil, "", err
	}
	baseline, err := a.observeStablePnPRegistrationCohort(ctx, config, source, wants)
	if err != nil {
		return nil, "", err
	}
	write, err := a.planPnPCohortRegistrationWrite(ctx, config, source, wants, want, baseline)
	return write, "", err
}

// Resolve a finalized registration for a later onboarding stage. The UUID is
// ownership evidence only, never permission to claim, reset or provision.
// An absent receipt returns an empty ID; an owned but missing/replaced native
// record is an error, so callers cannot silently recreate it.
func (a *adapter) resolveOwnedPnPRegistration(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want pnpDeviceIntent) (string, error) {
	if a == nil || config == nil || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" || len(config.Spec.SecretRefs) != 0 {
		return "", errConfigurationTransition
	}
	if err := declaredPnPRegistration(config, source, want); err != nil {
		return "", err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return "", err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return "", err
	}
	state := nc.Status.Configuration
	if state == nil {
		return "", nil
	}
	if state.Operation != nil {
		return "", errConfigurationBusy
	}
	retained, err := ownedPnPRegistrationReceipt(nc, config, want)
	if err != nil {
		return "", err
	}
	if retained == nil {
		return "", nil
	}
	// A later manifest revision may change claim intent while keeping the
	// registration unchanged. Its current snapshot, not the historical source
	// revision, is checked on both sides of authoritative native readback.
	done, err := a.client.observePnPRegistrationOperation(ctx, want, retained)
	if err != nil {
		return "", err
	}
	if !done {
		return "", errConfigurationBusy
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return "", err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return "", err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
		return "", errConfigurationStale
	}
	return retained.CreatedResourceID, nil
}

// Inspect retained registration ownership independently of the active claim
// slot. Callers must bracket native reads with source/operation snapshot checks.
func ownedPnPRegistrationReceipt(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, want pnpDeviceIntent) (*cvk.NetworkControllerConfigurationOperation, error) {
	if nc == nil || config == nil {
		return nil, errConfigurationTransition
	}
	body, err := pnpRegistrationPayload(want)
	if err != nil {
		return nil, err
	}
	resource := fmt.Sprintf("pnp-register:%x", sha256.Sum256(body))
	state := nc.Status.Configuration
	if state == nil {
		return nil, nil
	}
	owners := 0
	for _, owner := range state.Owners {
		if owner.Section != "inventory" {
			continue
		}
		if owner.ConfigUID != string(config.UID) || owner.ConfigName != config.Name {
			return nil, errConfigurationOwned
		}
		owners++
	}
	var retained *cvk.NetworkControllerConfigurationOperation
	for _, receipt := range state.ActionApplications {
		if receipt.Operation.Resource != resource {
			continue
		}
		op := receipt.Operation.DeepCopy()
		if retained != nil || owners != 1 || !pnpRegistrationReceiptPresent(state, op) || op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ConfigGeneration < 1 || op.ConfigGeneration > config.Generation || op.ControllerGeneration != nc.Generation {
			return nil, errConfigurationOwned
		}
		retained = op
	}
	return retained, nil
}
