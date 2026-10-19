// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
)

// Bind native observation to the current source, owner and exact retained
// journal. The shared reconciler separately validates physical admission and
// controls completion publication; this function only returns evidence.
func (a *adapter) observeOwnedTemplateDeployment(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, device inventoryDeviceIntent, target regularTemplateTarget, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	return a.observeOwnedInventoryDeployment(ctx, config, source, op, func(ctx context.Context) (bool, error) {
		return a.client.observeRegularTemplateDeployment(ctx, device, target, op, now)
	})
}

func (a *adapter) observeOwnedCompositeTemplateDeployment(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, device inventoryDeviceIntent, target compositeTemplateTarget, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	return a.observeOwnedInventoryDeployment(ctx, config, source, op, func(ctx context.Context) (bool, error) {
		return a.client.observeCompositeTemplateDeployment(ctx, device, target, op, now)
	})
}

// Common owner/source/journal bracket for native inventory observations. This
// does not grant physical admission or advance completion state.
func (a *adapter) observeOwnedInventoryDeployment(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation, observe func(context.Context) (bool, error)) (bool, error) {
	return a.observeOwnedConfigurationOperation(ctx, config, source, op, "inventory", observe)
}

// Shared read-only source/owner/journal bracket. Product observers must still
// validate their complete manifest and native completion contract.
func (a *adapter) observeOwnedConfigurationOperation(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation, section string, observe func(context.Context) (bool, error)) (bool, error) {
	networkMetadata := section == "network_settings" && (networkSettingKnownTaskSupported(op) || networkSettingApplicationEnvelope(op) && op.Phase == "Verifying")
	profileMetadata := section == "network_profiles" && (networkProfileMetadataEnvelope(op) || (profileSiteApplicationEnvelope(op) || wirelessTagPrivateEnvelope(op) || validSwitchingCreateOperation(op)) && op.Phase == "Verifying")
	if a == nil || config == nil || op == nil || observe == nil || (!networkMetadata && !profileMetadata && section != "inventory" && section != "fabric" && !(section == "lan_automation" && supportedLANMutationConfig(config) && (validLANLinkMutationResource(op.Resource, op.Action) || validLANSessionStartResource(op.Resource, op.Action)))) || op.Section != section || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != section || len(config.Spec.SecretRefs) != 0 && !(networkMetadata || profileMetadata && wirelessTagComparisonConfig(config) && source != nil && source.secrets != nil) {
		return false, errConfigurationTransition
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return false, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return false, err
	}
	uid, version := "", ""
	if source != nil {
		uid = string(source.uid)
		version = source.version
	}
	if nc.Status.Configuration == nil || !apiequality.Semantic.DeepEqual(nc.Status.Configuration.Operation, op) || op.ConfigName != config.Name || op.ConfigUID != string(config.UID) || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != uid || op.SourceVersion != version {
		return false, errConfigurationStale
	}
	owners := 0
	for _, owner := range nc.Status.Configuration.Owners {
		if owner.Section == section {
			if owner.ConfigUID != string(config.UID) || owner.ConfigName != config.Name {
				return false, errConfigurationOwned
			}
			owners++
		}
	}
	if owners != 1 {
		return false, errConfigurationOwned
	}
	done, err := observe(ctx)
	if err != nil || !done {
		return false, err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return false, err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return false, err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion || fresh.Status.Configuration == nil || !apiequality.Semantic.DeepEqual(fresh.Status.Configuration.Operation, op) {
		return false, errConfigurationStale
	}
	return true, nil
}

// Record a completed deployment through the existing atomic action-receipt
// writer. Native verification precedes bookkeeping; this never submits a CLI
// deployment or releases a physical reservation.
func (a *adapter) recordOwnedTemplateDeployment(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, device inventoryDeviceIntent, target regularTemplateTarget, operationID string, now time.Time) error {
	return a.recordOwnedInventoryDeployment(ctx, config, source, operationID, func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
		return a.observeOwnedTemplateDeployment(ctx, config, source, device, target, op, now)
	})
}
func (a *adapter) recordOwnedCompositeTemplateDeployment(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, device inventoryDeviceIntent, target compositeTemplateTarget, operationID string, now time.Time) error {
	return a.recordOwnedInventoryDeployment(ctx, config, source, operationID, func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
		return a.observeOwnedCompositeTemplateDeployment(ctx, config, source, device, target, op, now)
	})
}

// Shared atomic completion bookkeeping, after owned native observation.
func (a *adapter) recordOwnedInventoryDeployment(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, operationID string, observe func(context.Context, *cvk.NetworkControllerConfigurationOperation) (bool, error)) error {
	if observe == nil {
		return errConfigurationTransition
	}
	return a.recordOwnedInventoryAction(ctx, config, source, operationID, func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (string, error) {
		done, err := observe(ctx, op)
		if err != nil || !done {
			return "", err
		}
		return op.Resource, nil
	})
}

// Resource-specific observation can bind a native result UUID into the receipt
// key while retaining the unchanged admitted operation and shared atomic writer.
func (a *adapter) recordOwnedInventoryAction(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, operationID string, observe func(context.Context, *cvk.NetworkControllerConfigurationOperation) (string, error)) error {
	if a == nil || config == nil || observe == nil || !configurationOperationID.MatchString(operationID) {
		return errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation
	if op.ID != operationID || op.Phase != "Verifying" {
		return errConfigurationTransition
	}
	receiptKey, err := observe(ctx, op)
	if err != nil {
		return err
	}
	if receiptKey == "" {
		return errConfigurationBusy
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
		return errConfigurationStale
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	op.Phase = "Verified"
	if receiptKey == op.Resource {
		err = a.retainPhysicalActionReceipt(ctx, nc)
	} else {
		err = a.retainConfigurationActionReceipt(ctx, nc, receiptKey)
	}
	if err != nil {
		return err
	}
	return a.persistConfigurationActionReceipt(ctx, nc)
}
