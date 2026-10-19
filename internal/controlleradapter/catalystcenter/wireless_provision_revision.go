// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// planWirelessProvisionRevision adds journal revision semantics to native
// planning. It does not grant device admission or enable manifest execution.
func (a *adapter) planWirelessProvisionRevision(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, ref configurationDeviceReference, state, siteID string, modelConfigRequired bool) (*configurationWrite, error) {
	return a.planBoundWirelessProvisionRevision(ctx, config, source, ref, state, siteID, modelConfigRequired, "")
}

func (a *adapter) planBoundWirelessProvisionRevision(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, ref configurationDeviceReference, state, siteID string, modelConfigRequired bool, dependencyDigest string) (*configurationWrite, error) {
	if config == nil || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" {
		return nil, errConfigurationTransition
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		return nil, errConfigurationBusy
	}
	write, err := a.client.planWirelessDeviceProvision(ctx, ref, state, siteID, modelConfigRequired)
	if err != nil {
		return nil, err
	}
	write, err = bindWirelessProvisionDependencies(write, dependencyDigest)
	if err != nil {
		return nil, err
	}
	// A task or source edit can arrive during controller inventory reads. Never
	// use the initial journal snapshot as proof that the operation slot is free.
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		return nil, errConfigurationBusy
	}
	desired := &cvk.NetworkControllerConfigurationOperation{ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, Section: "inventory", Resource: write.resource, Action: write.action, TargetID: write.identity.target, TargetSerial: write.identity.serial}
	if source != nil {
		desired.SourceUID = string(source.uid)
		desired.SourceVersion = source.version
	}
	applied, err := wirelessProvisionAlreadyApplied(nc.Status.Configuration, desired)
	if err != nil {
		return nil, err
	}
	if applied {
		return nil, nil
	}
	return write, nil
}
