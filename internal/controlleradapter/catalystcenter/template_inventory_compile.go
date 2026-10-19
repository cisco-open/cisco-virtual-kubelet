// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"reflect"
)

// Existing qualified regular templates, inventory-only composition behind
// the existing physical admission hook. Full lab qualification is separate.
func (a *adapter) compileTemplateInventoryApply(config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	return a.compileInventoryTemplateKinds(config, source, false)
}

// Mixed inventory composition uses the same physical admission hook.
func (a *adapter) compileCompositeInventoryApply(config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	return a.compileInventoryTemplateKinds(config, source, true)
}
func (a *adapter) compileInventoryTemplateKinds(config *configapi.NetworkControllerConfig, source *configSourceSnapshot, allowComposite bool) (configurationApplyPlan, error) {
	var zero configurationApplyPlan
	if !a.configurationAdmissionReady() || config == nil || source == nil || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" || len(config.Spec.SecretRefs) != 0 {
		return zero, errConfigurationDeviceAdmission
	}
	intent, states, targets, err := parseTemplateInventory(source.raw, source.raw, allowComposite)
	if err != nil {
		return zero, err
	}
	prerequisites, err := a.compileWiredInventoryIntent(config, source, intent, states)
	if err != nil {
		return zero, err
	}
	stages := &templateInventoryStages{adapter: a, prerequisites: prerequisites}
	// count() must be meaningful before the first native observation.
	for _, device := range intent.devices {
		for _, target := range targets[device.reference.Name].regular {
			target.comparison = source.templateComparison
			stages.templates = append(stages.templates, templateStageBinding{executor: &regularTemplateExecutor{adapter: a, config: config, source: source, device: device, target: target}})
		}
		for _, target := range targets[device.reference.Name].composite {
			target.comparison = source.templateComparison
			stages.templates = append(stages.templates, templateStageBinding{executor: &compositeTemplateExecutor{adapter: a, config: config, source: source, device: device, target: target}})
		}
	}
	type stableBinding struct{ device, serial, site, base string }
	var baseline []stableBinding
	var provisionBindings map[string]wiredInventoryBinding
	stages.validate = func(ctx context.Context) error {
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		devices, err := a.client.wiredInventoryBindings(ctx, intent, states)
		if err != nil {
			return err
		}
		if provisionBindings != nil && !reflect.DeepEqual(provisionBindings, devices) {
			return errConfigurationStale
		}
		inventory, err := a.client.configurationDevices(ctx)
		if err != nil {
			return err
		}
		projects, err := a.client.listTemplateProjects(ctx)
		if err != nil {
			return err
		}
		definitions, err := a.client.listTemplateDefinitions(ctx)
		if err != nil {
			return err
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		var recorded *templateReceiptVersionBinding
		if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && isTemplateDeploymentResource(nc.Status.Configuration.Operation.Resource) {
			recorded, err = templateReceiptBinding(nc.Status.Configuration.Operation)
			if err != nil {
				return err
			}
		}
		resolved := make([]stableBinding, 0, len(stages.templates))
		for i := range stages.templates {
			entry := &stages.templates[i]
			var desiredDevice inventoryDeviceIntent
			var identity nacmodel.TemplateIdentity
			switch e := entry.executor.(type) {
			case *regularTemplateExecutor:
				if e == nil {
					return errConfigurationTransition
				}
				desiredDevice, identity = e.device, e.target.template
			case *compositeTemplateExecutor:
				if e == nil {
					return errConfigurationTransition
				}
				desiredDevice, identity = e.device, e.target.template
			default:
				return errConfigurationTransition
			}
			physical, ok := devices[desiredDevice.reference.Name]
			if !ok {
				return errConfigurationStale
			}
			device, err := resolveConfigurationDevice(desiredDevice.reference, inventory)
			if err != nil || device.ID != physical.deviceID {
				return errConfigurationStale
			}
			project, ok := projects[identity.Project]
			if !ok {
				return errConfigurationBusy
			}
			definition, ok := definitions[project.ID+"#"+identity.Name]
			if !ok {
				return errConfigurationBusy
			}
			matching := recorded != nil && recorded.kind == entry.executor.deploymentKind() && recorded.baseID == definition.ID && nc.Status.Configuration.Operation.TargetID == physical.deviceID
			platform, err := a.client.templateDevicePlatform(ctx, device)
			if err != nil {
				return err
			}
			if platform.Serial != physical.serial {
				return errConfigurationStale
			}
			switch e := entry.executor.(type) {
			case *regularTemplateExecutor:
				var pinned *templateDeploymentVersionBinding
				if matching {
					pinned, err = parseTemplateDeploymentResource(nc.Status.Configuration.Operation.Resource)
					if err != nil {
						return err
					}
				}
				content, err := a.client.regularTemplateContent(ctx, identity, pinned)
				if err != nil {
					return err
				}
				if content.base.ID != definition.ID || content.project.ID != project.ID {
					return errConfigurationStale
				}
				if err := validateTemplatePlatform(content.body, platform); err != nil {
					return err
				}
				if err := validateTemplateDeploymentParameters(content.body, e.target.parameters); err != nil {
					return err
				}
			case *compositeTemplateExecutor:
				var pinned *compositeDeploymentVersionBinding
				if matching {
					pinned, err = parseCompositeDeploymentBindings(nc.Status.Configuration.Operation.DependencyVersions)
					if err != nil {
						return err
					}
				}
				content, err := a.client.compositeTemplateContent(ctx, identity, pinned)
				if err != nil {
					return err
				}
				if content.parent.base.ID != definition.ID || content.parent.project.ID != project.ID {
					return errConfigurationStale
				}
				if _, err := content.payload(platform, e.target.copyingConfig, e.target.forcePush, e.target.parameters); err != nil {
					return err
				}
			}
			resolved = append(resolved, stableBinding{physical.deviceID, physical.serial, physical.siteID, definition.ID})
		}
		if baseline != nil && !reflect.DeepEqual(baseline, resolved) {
			return errConfigurationStale
		}
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		baseline = resolved
		provisionBindings = devices
		for i, binding := range resolved {
			stages.templates[i].deviceID = binding.device
			stages.templates[i].serial = binding.serial
			stages.templates[i].baseID = binding.base
			switch e := stages.templates[i].executor.(type) {
			case *regularTemplateExecutor:
				e.device.reference.Serial = binding.serial
			case *compositeTemplateExecutor:
				e.device.reference.Serial = binding.serial
			}
		}
		return nil
	}
	stages.settle = func(ctx context.Context, write *configurationWrite) error {
		provisioned := map[string]wiredInventoryBinding{}
		for name := range states {
			binding, exists := provisionBindings[name]
			if !exists {
				return errConfigurationStale
			}
			provisioned[name] = binding
		}
		return a.templateProvisionSettling(ctx, config, source, provisioned, write)
	}
	plan := prerequisites
	plan.executor = stages
	plan.observeCompletion = stages.observeCompletion
	plan.sections = append([]configapi.NetworkControllerSectionStatus{}, prerequisites.sections...)
	plan.sections[0].ResourceCount = int64(stages.count())
	return plan, nil
}
