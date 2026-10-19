// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"reflect"
	"strings"
)

// Assemble standalone wireless stages behind the same physical admission hook.
// Shared manifest reconciliation owns submission, task polling and recovery.
func (a *adapter) compileWirelessInventoryApply(config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	var empty configurationApplyPlan
	if !a.configurationAdmissionReady() || config == nil || source == nil || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" || len(config.Spec.SecretRefs) != 0 {
		return empty, errConfigurationDeviceAdmission
	}
	intent, err := parseInventoryIntent(source.raw)
	if err != nil {
		return empty, err
	}
	wireless := []inventoryDeviceIntent{}
	for _, device := range intent.devices {
		if device.wireless == nil {
			continue
		}
		if device.wireless.deviceType != "WirelessController" || len(device.wireless.roles) != 0 || device.wireless.fabricSite != "" {
			return empty, errConfigurationTransition
		}
		wireless = append(wireless, device)
	}
	if len(wireless) == 0 {
		return a.client.compileManagedInventoryApply(source.raw)
	}
	base, err := a.client.managedInventoryPlan(intent)
	if err != nil {
		return empty, err
	}
	group, ok := base.executor.(*configurationGroup)
	if !ok {
		return empty, errConfigurationTransition
	}
	stages := &wirelessInventoryStages{prerequisites: group}
	executors := make([]*wirelessProvisionExecutor, 0, len(wireless))
	for _, device := range wireless {
		key, err := wirelessTagDigest(device.reference.Name)
		if err != nil {
			return empty, err
		}
		provision := &wirelessProvisionExecutor{adapter: a, config: config, source: source, reference: device.reference, state: device.wireless.state}
		location := device.wireless.locations
		for _, anchor := range []bool{false, true} {
			if !anchor && location.primary == nil && location.secondary == nil || anchor && location.anchor == nil {
				continue
			}
			prefix := "ap-" + key[:16]
			if anchor {
				prefix = "anchor-" + key[:16]
			}
			executor := &wirelessManagedAPExecutor{client: a.client, reference: device.reference, desired: location, anchor: anchor}
			group.parts = append(group.parts, configurationPart{name: prefix, executor: executor})
			provision.apDependencies = append(provision.apDependencies, wirelessProvisionAPDependency{prefix: prefix + "/", executor: executor})
		}
		stages.provisioning = append(stages.provisioning, provision)
		executors = append(executors, provision)
	}
	var bound map[string]string
	stages.validate = func(ctx context.Context) error {
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		snapshot, err := a.client.wirelessInventoryDependencies(ctx)
		if err != nil {
			return err
		}
		bindings, err := a.client.validateStandaloneWirelessSnapshot(ctx, snapshot, wireless)
		if err != nil {
			return err
		}
		for i, device := range wireless {
			_, placement, err := snapshot.standalonePlacement(device)
			if err != nil {
				return err
			}
			executors[i].siteID = snapshot.sites[device.site].ID
			executors[i].modelConfigRequired = false
			for key := range placement {
				if !strings.HasPrefix(key, "profile:") {
					continue
				}
				profile := snapshot.profiles[strings.TrimPrefix(key, "profile:")]
				if ssids, _ := profile.fields["ssidDetails"].([]map[string]any); len(ssids) > 0 {
					executors[i].modelConfigRequired = true
				}
			}
		}
		if bound != nil && !reflect.DeepEqual(bound, bindings) {
			return errConfigurationStale
		}
		digest, err := wirelessTagDigest(bindings)
		if err != nil {
			return err
		}
		for _, executor := range executors {
			executor.dependencyDigest = digest
		}
		bound = bindings
		return nil
	}
	for _, executor := range executors {
		executor.validateDependencies = stages.validate
	}
	base.executor = stages
	base.sections[0].ResourceCount = int64(stages.count())
	return base, nil
}
