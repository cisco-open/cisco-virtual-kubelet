// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Compile the complete model; mapping execution may not silently drop pending
// fabric, multicast or other prerequisite effects declared by the same source.
func (a *adapter) declaredFabricVLANSSIDs(ctx context.Context, source *configSourceSnapshot, path string) ([]fabricVLANSSID, error) {
	if a == nil || a.client == nil || source == nil {
		return nil, errConfigurationTransition
	}
	plan, err := a.client.compileFabricConfiguration(source.raw)
	if err != nil {
		return nil, err
	}
	mappings, ok := plan.configurationExecutor.(*fabricVLANSSIDConfiguration)
	if !ok || len(plan.reports) == 0 {
		return nil, errConfigurationTransition
	}
	var want []fabricVLANSSID
	for _, site := range mappings.desired {
		if site.path != path {
			continue
		}
		if want != nil {
			return nil, errConfigurationStale
		}
		want, err = canonicalFabricVLANSSIDs(site.mappings)
		if err != nil {
			return nil, err
		}
	}
	if len(want) == 0 {
		return nil, errConfigurationStale
	}
	// The compiler appends mapping reporting after every foundation report.
	for _, report := range plan.reports[:len(plan.reports)-1] {
		drift, err := report(ctx)
		if err != nil {
			return nil, err
		}
		for _, entry := range drift {
			// Wireless multicast is a downstream stage; every other effect must
			// already match before mapping mutations are admitted.
			if entry.Section != "fabric" || entry.Path != "/fabric_sites/multicast/wireless_multicast_enabled" || entry.Change != configapi.NetworkControllerDriftChangeUpdate || !strings.HasPrefix(entry.Resource, "fabric-wireless-multicast:") {
				return nil, errConfigurationBusy
			}
		}
	}
	return want, nil
}

func (a *adapter) prepareFabricVLANSSIDs(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, path string) (*cvk.NetworkControllerConfigurationOperation, error) {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "fabric" || len(config.Spec.SecretRefs) != 0 {
		return nil, errConfigurationTransition
	}
	want, err := a.declaredFabricVLANSSIDs(ctx, source, path)
	if err != nil {
		return nil, err
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
	var applications []cvk.NetworkControllerConfigurationActionApplication
	if nc.Status.Configuration != nil {
		for _, owner := range nc.Status.Configuration.Owners {
			if owner.Section == "fabric" && (owner.ConfigUID != string(config.UID) || owner.ConfigName != config.Name) {
				return nil, errConfigurationOwned
			}
		}
		applications = nc.Status.Configuration.ActionApplications
	}
	observed, err := a.client.observeFabricVLANSSIDs(ctx, path)
	if err != nil {
		return nil, err
	}
	owned, err := fabricVLANSSIDOwnership(path, observed.siteID, observed.fabricID, string(config.UID), observed.mappings, applications)
	if err != nil {
		return nil, err
	}
	write, targets, err := a.client.planManagedFabricVLANSSIDs(ctx, a.statusReader, config.Namespace, path, want, owned)
	if err != nil {
		return nil, err
	}
	if write != nil && (write.identity.target != observed.fabricID || write.identity.parent != observed.siteID) {
		return nil, errConfigurationStale
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
		return nil, errConfigurationStale
	}
	if write == nil {
		return nil, nil
	}
	identity := write.identity
	identity.groupTargets = targets
	return a.prepareConfiguration(ctx, config, source, "fabric", write.resource, write.action, identity)
}
