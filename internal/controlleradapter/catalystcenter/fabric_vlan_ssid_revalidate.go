// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"reflect"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

func (a *adapter) currentFabricVLANSSIDOwnership(ctx context.Context, config *configapi.NetworkControllerConfig, path string) (map[string]fabricVLANSSIDOwned, error) {
	return a.currentFabricVLANSSIDOperationOwnership(ctx, config, path, nil)
}

func (a *adapter) currentFabricVLANSSIDOperationOwnership(ctx context.Context, config *configapi.NetworkControllerConfig, path string, operation *cvk.NetworkControllerConfigurationOperation) (map[string]fabricVLANSSIDOwned, error) {
	if a == nil || a.client == nil || config == nil {
		return nil, errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.Status.Configuration == nil {
		return nil, errConfigurationOwned
	}
	count := 0
	for _, owner := range nc.Status.Configuration.Owners {
		if owner.Section != "fabric" {
			continue
		}
		if owner.ConfigUID != string(config.UID) || owner.ConfigName != config.Name {
			return nil, errConfigurationOwned
		}
		count++
	}
	if count != 1 {
		return nil, errConfigurationOwned
	}
	observed, err := a.client.observeFabricVLANSSIDs(ctx, path)
	if err != nil {
		return nil, err
	}
	var owned map[string]fabricVLANSSIDOwned
	if operation == nil {
		owned, err = fabricVLANSSIDOwnership(path, observed.siteID, observed.fabricID, string(config.UID), observed.mappings, nc.Status.Configuration.ActionApplications)
	} else {
		if !reflect.DeepEqual(nc.Status.Configuration.Operation, operation) {
			return nil, errConfigurationStale
		}
		owned, err = fabricVLANSSIDRecoveryOwnership(path, observed.siteID, observed.fabricID, string(config.UID), observed.mappings, nc.Status.Configuration.ActionApplications, operation)
	}
	if err != nil {
		return nil, err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
		return nil, errConfigurationStale
	}
	return owned, nil
}

func (a *adapter) revalidateFabricVLANSSIDOperation(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, path string, op *cvk.NetworkControllerConfigurationOperation, phase string) error {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || op == nil || (phase != "Prepared" && phase != "Submitting") || op.Phase != phase || op.Section != "fabric" || op.Action != "Update" || op.TaskID != "" || op.CreatedResourceID != "" || op.CancellationRequestedAt != nil || (phase == "Prepared" && op.SubmittedAt != nil) || (phase == "Submitting" && (op.SubmittedAt == nil || op.SubmittedAt.IsZero())) {
		return errConfigurationTransition
	}
	if _, err := parseFabricVLANSSIDResource(op.Resource); err != nil {
		return err
	}
	if err := contract.ValidateGroupJournalIdentity(op); err != nil {
		return errConfigurationStale
	}
	want, err := a.declaredFabricVLANSSIDs(ctx, source, path)
	if err != nil {
		return err
	}
	ok, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
		owned, err := a.currentFabricVLANSSIDOwnership(ctx, config, path)
		if err != nil {
			return false, err
		}
		write, targets, err := a.client.planManagedFabricVLANSSIDs(ctx, a.statusReader, config.Namespace, path, want, owned)
		if err != nil {
			return false, err
		}
		if write == nil || write.resource != op.Resource || write.action != op.Action || write.identity.target != op.TargetID || write.identity.parent != op.ParentID || !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
			return false, errConfigurationStale
		}
		return true, nil
	})
	if err != nil {
		return err
	}
	if !ok {
		return errConfigurationBusy
	}
	return nil
}
