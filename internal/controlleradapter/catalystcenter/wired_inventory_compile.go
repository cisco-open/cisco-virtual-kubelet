// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"reflect"
)

// Preserve imperative state separately while reusing common inventory model
// validation. Fabric/wireless fields are not stripped or silently discarded.
func parseWiredInventoryIntent(raw []byte) (inventoryIntent, map[string]string, error) {
	var zero inventoryIntent
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return zero, nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return zero, nil, errInvalidResponse
	}
	inventory, ok := root["inventory"].(map[string]any)
	if !ok {
		return zero, nil, errInvalidResponse
	}
	rows, ok := inventory["devices"].([]any)
	if !ok || len(rows) == 0 {
		return zero, nil, errInvalidResponse
	}
	states := map[string]string{}
	for _, row := range rows {
		device, ok := row.(map[string]any)
		if !ok {
			return zero, nil, errInvalidResponse
		}
		state, ok := device["state"].(string)
		if !ok {
			return zero, nil, errInvalidResponse
		}
		switch state {
		case "ASSIGN":
		case "PROVISION", "REPROVISION":
			name, ok := device["name"].(string)
			if !ok || name == "" || states[name] != "" {
				return zero, nil, errInvalidResponse
			}
			states[name] = state
			device["state"] = "ASSIGN"
		default:
			return zero, nil, errInvalidResponse
		}
	}
	if len(states) == 0 {
		return zero, nil, errInvalidResponse
	}
	projection, err := json.Marshal(map[string]any{"catalyst_center": root})
	if err != nil {
		return zero, nil, err
	}
	common, err := parseInventoryIntent(projection)
	return common, states, err
}

type wiredInventoryBinding struct{ deviceID, serial, siteID string }

func (c *client) wiredInventoryBindings(ctx context.Context, intent inventoryIntent, states map[string]string) (map[string]wiredInventoryBinding, error) {
	devices, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	byPath := map[string]Site{}
	for _, site := range sites {
		byPath[site.NameHierarchy] = site
	}
	out := map[string]wiredInventoryBinding{}
	seen := map[string]bool{}
	for _, want := range intent.devices {
		target, err := resolveConfigurationDevice(want.reference, devices)
		if err != nil {
			return nil, err
		}
		physical, err := bindMulticastRPDevices([]string{target.ID}, devices)
		if err != nil {
			return nil, err
		}
		target = physical[0]
		if seen[target.Serial] {
			return nil, errConfigurationOwned
		}
		seen[target.Serial] = true
		if states[want.reference.Name] != "" && (target.Reachability != "Reachable" || (target.Family != "Switches and Hubs" && target.Family != "Routers")) {
			return nil, errConfigurationTransition
		}
		site, exists := byPath[want.site]
		if !exists || !validAPIID(site.ID) || site.Type == "global" {
			return nil, errConfigurationBusy
		}
		out[want.reference.Name] = wiredInventoryBinding{target.ID, target.Serial, site.ID}
	}
	return out, nil
}

// Compile wired inventory only behind the existing physical admission hook.
func (a *adapter) compileWiredInventoryApply(config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	var zero configurationApplyPlan
	if !a.configurationAdmissionReady() || config == nil || source == nil || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" || len(config.Spec.SecretRefs) != 0 {
		return zero, errConfigurationDeviceAdmission
	}
	intent, states, err := parseWiredInventoryIntent(source.raw)
	if err != nil {
		return zero, err
	}
	return a.compileWiredInventoryIntent(config, source, intent, states)
}

// Share parsed inventory execution without replacing the original source snapshot.
func (a *adapter) compileWiredInventoryIntent(config *configapi.NetworkControllerConfig, source *configSourceSnapshot, intent inventoryIntent, states map[string]string) (configurationApplyPlan, error) {
	var zero configurationApplyPlan
	base, err := a.client.managedInventoryPlan(intent)
	if err != nil {
		return zero, err
	}
	stages := &wiredInventoryStages{adapter: a, prerequisites: base.executor}
	for _, device := range intent.devices {
		if state := states[device.reference.Name]; state != "" {
			stages.provisioning = append(stages.provisioning, &wiredProvisionExecutor{adapter: a, config: config, source: source, desired: device, state: state})
		}
	}
	var baseline map[string]wiredInventoryBinding
	stages.validate = func(ctx context.Context) error {
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		bindings, err := a.client.wiredInventoryBindings(ctx, intent, states)
		if err != nil {
			return err
		}
		if baseline != nil && !reflect.DeepEqual(bindings, baseline) {
			return errConfigurationStale
		}
		baseline = bindings
		return nil
	}
	base.executor = stages
	base.sections[0].ResourceCount = int64(stages.count())
	return base, nil
}

// Select only a fully validated wired projection. Existing assignment and
// wireless models retain their current compiler; malformed or mixed intent
// cannot fall through to a tag-only or assignment-only execution path.
func (a *adapter) compileAdmittedInventoryApply(config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	if source != nil {
		if _, _, _, err := parseRMAInventory(source.raw); err == nil {
			return a.compileRMAMarkInventoryApply(config, source)
		}
		if _, _, _, err := parseRegularTemplateInventory(source.raw, source.raw); err == nil {
			return a.compileTemplateInventoryApply(config, source)
		}
		if _, _, _, err := parseTemplateInventory(source.raw, source.raw, true); err == nil {
			return a.compileCompositeInventoryApply(config, source)
		}
		if _, _, err := parseWiredInventoryIntent(source.raw); err == nil {
			return a.compileWiredInventoryApply(config, source)
		}
	}
	return a.compileWirelessInventoryApply(config, source)
}
