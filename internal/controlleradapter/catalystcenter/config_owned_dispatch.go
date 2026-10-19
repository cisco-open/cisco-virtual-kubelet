// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"slices"
	"time"
)

// Reobserve the shared durable submission permit after resource-native preflight.
// Resource semantics supply only their operation validator and receipt capacity check.
func (a *adapter) checkOwnedConfigurationDispatch(ctx context.Context, nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, section, resource, action string, identity configurationResourceIdentity, valid func(*cvk.NetworkControllerConfigurationOperation) bool, capacity func(*cvk.NetworkControllerConfigurationStatus, *cvk.NetworkControllerConfigurationOperation) error) error {
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	current, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if current.UID != nc.UID || current.Generation != nc.Generation || current.Spec.Paused || !current.DeletionTimestamp.IsZero() || current.Status.Configuration == nil {
		return errConfigurationStale
	}
	state := current.Status.Configuration
	if pnpRemovalSelected(resource) && a.metadataAdmissionReady() && state.MetadataHandoff == nil {
		return errConfigurationBusy
	}
	if state.MetadataHandoff != nil {
		if identity.metadata == nil || identity.metadata.permit == nil {
			return errConfigurationBusy
		}
		if err := identity.metadata.permit.Reobserve(ctx, time.Now()); err != nil {
			return err
		}
	}
	op := state.Operation
	if !slices.Contains(config.Spec.ManagedSections, section) || !valid(op) || op.Phase != "Submitting" || op.Resource != resource || op.Action != action || op.Section != section || op.TargetID != identity.target || op.ParentID != identity.parent || op.TaskID != "" || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) || op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
		return errConfigurationStale
	}
	if err := configurationOwnersMatch(state, config); err != nil {
		return err
	}
	if (discoveryCredentialInputSelected(resource) || discoveryCredentialRetirementSelected(resource)) && discoveryCredentialCustodyRequired(current, string(config.UID)) {
		if current.ResourceVersion != nc.ResourceVersion {
			return errConfigurationStale
		}
		if action == "Delete" && discoveryFinalizeSelected(resource) {
			if _, err := discoveryCredentialCleanupHistory(current, config, source, op); err != nil {
				return err
			}
		} else if action != "Create" && !(action == "Delete" && config.DeletionTimestamp.IsZero() && discoveryCredentialRetirementSelected(resource)) {
			return errConfigurationOwned
		} else if _, err := discoveryCredentialExecutionHistory(current, config, source); err != nil {
			return err
		}
	}
	return capacity(state, op)
}

func configurationInputAfter(op, previous *cvk.NetworkControllerConfigurationOperation) bool {
	return op.ConfigUID == previous.ConfigUID && (op.ConfigGeneration > previous.ConfigGeneration || op.ConfigGeneration == previous.ConfigGeneration && (op.SourceUID != previous.SourceUID || op.SourceVersion != previous.SourceVersion))
}

func configurationOwnersMatch(state *cvk.NetworkControllerConfigurationStatus, config *configapi.NetworkControllerConfig) error {
	if len(config.Spec.ManagedSections) == 0 {
		return errConfigurationOwned
	}
	seen := map[string]bool{}
	for _, section := range config.Spec.ManagedSections {
		if seen[section] {
			return errConfigurationOwned
		}
		seen[section] = true
		count := 0
		for _, owner := range state.Owners {
			if configurationSectionsOverlap(owner.Section, section) && (owner.ConfigUID != string(config.UID) || owner.ConfigName != config.Name) {
				return errConfigurationOwned
			}
			if owner.Section == section {
				if owner.ConfigUID != string(config.UID) || owner.ConfigName != config.Name {
					return errConfigurationOwned
				}
				count++
			}
		}
		if count != 1 {
			return errConfigurationOwned
		}
	}
	return nil
}
