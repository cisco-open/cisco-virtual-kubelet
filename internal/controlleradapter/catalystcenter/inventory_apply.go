// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"errors"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

// Compile only behind the runtime's physical configuration admission hook.
// Reuse resource executors and the shared group/journal; no separate task loop.
// All intent is parsed before returning a plan, including unsupported actions.
func (c *client) compileManagedInventoryApply(raw []byte) (configurationApplyPlan, error) {
	var plan configurationApplyPlan
	intent, err := parseInventoryIntent(raw)
	if err != nil {
		return plan, err
	}
	if len(intent.devices) == 0 {
		return c.compileConfigurationApply(raw, []string{"inventory"}, nil)
	}
	for _, device := range intent.devices {
		if device.wireless != nil {
			return plan, errors.New("wireless provisioning requires specialized manifest recovery")
		}
	}
	return c.managedInventoryPlan(intent)
}

// Build shared non-provisioning stages after a caller has validated device kind.
func (c *client) managedInventoryPlan(intent inventoryIntent) (configurationApplyPlan, error) {
	var plan configurationApplyPlan
	members := []desiredDeviceMembership{}
	for _, device := range intent.devices {
		if device.reference.Serial == "" {
			return plan, errors.New("managed inventory apply requires explicit physical serial identity")
		}
		if len(device.reference.Name)+len("assignments/") > 512 {
			return plan, errConfigurationTransition
		}
		if len(device.tags) > 0 {
			members = append(members, desiredDeviceMembership{device: device.reference, tags: device.tags})
		}
	}
	parts := []configurationPart{}
	if len(intent.tags) > 0 {
		for _, tag := range intent.tags {
			if tag.payload["systemTag"] == true {
				return plan, errors.New("system tag creation is not qualified")
			}
		}
		parts = append(parts, configurationPart{name: "tags", executor: &tagExecutor{client: c, desired: intent.tags}})
	}
	if len(intent.discoveries) > 0 {
		parts = append(parts, configurationPart{name: "discoveries", executor: &discoveryExecutor{client: c, desired: intent.discoveries}})
	}
	parts = append(parts, configurationPart{name: "assignments", executor: &deviceAssignmentExecutor{client: c, desired: intent.devices}})
	roles := &deviceRoleExecutor{client: c, desired: intent.devices}
	if roles.count() > 0 {
		parts = append(parts, configurationPart{name: "roles", executor: roles})
	}
	if len(members) > 0 {
		parts = append(parts, configurationPart{name: "memberships", executor: &deviceMembershipExecutor{client: c, devices: members, declared: intent.tags}})
	}
	executor := &configurationGroup{parts: parts}
	return configurationApplyPlan{executor: executor, section: "inventory", sections: []configapi.NetworkControllerSectionStatus{{Name: "inventory", ResourceCount: int64(executor.count())}}}, nil
}
