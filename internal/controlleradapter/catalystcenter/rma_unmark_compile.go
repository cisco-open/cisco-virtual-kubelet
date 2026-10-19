// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	"strings"
)

// Historical RMA effects determine whether removing RMA fields means cleanup.
// Without that history ordinary inventory keeps its existing compiler. Runtime
// executors re-read authority; this snapshot only selects the resource semantics.
func (a *adapter) compileOwnedInventoryApply(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	history := false
	if nc != nil && nc.Status.Configuration != nil {
		for _, receipt := range nc.Status.Configuration.ActionApplications {
			if strings.HasPrefix(receipt.Operation.Resource, "rma-mark:") || strings.HasPrefix(receipt.Operation.Resource, "rma-unmark:") {
				history = true
			}
		}
		if op := nc.Status.Configuration.Operation; op != nil && strings.HasPrefix(op.Resource, "rma-unmark:") {
			history = true
		}
	}
	if history && source != nil {
		return a.compileOwnedRMAInventoryApply(nc, config, source)
	}
	if config != nil && config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete {
		if source != nil {
			if _, _, _, err := parseRMAInventory(source.raw); err == nil {
				return a.compileRMAMarkInventoryApply(config, source)
			}
		}
		return configurationApplyPlan{}, errConfigurationTransition
	}
	return a.compileAdmittedInventoryApply(config, source)
}

// Compose explicit marks and owned removal of marks on still-declared devices.
// Every target must describe one of those effects; unrelated provisioning fields
// are not discarded. Whole-device removal and REPLACE remain separate flows.
func (a *adapter) compileOwnedRMAInventoryApply(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	var zero configurationApplyPlan
	if a == nil || !a.configurationAdmissionReady() || nc == nil || nc.Status.Configuration == nil || config == nil || !supportedConfigurationApply(config) || source == nil || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" || len(config.Spec.SecretRefs) != 0 {
		return zero, errConfigurationDeviceAdmission
	}
	inventory, _, actions, err := parseRMAInventoryProjection(source.raw, false)
	if err != nil {
		return zero, err
	}
	if len(inventory.tags) != 0 || len(inventory.discoveries) != 0 {
		return zero, errInvalidResponse
	}
	retained := map[string]bool{}
	for _, receipt := range nc.Status.Configuration.ActionApplications {
		op := receipt.Operation
		if !strings.HasPrefix(op.Resource, "rma-mark:") && !strings.HasPrefix(op.Resource, "rma-unmark:") {
			continue
		}
		if op.ConfigName != config.Name || op.ConfigUID != string(config.UID) || op.TargetSerial == "" {
			return zero, errConfigurationOwned
		}
		retained[op.TargetSerial] = true
	}
	stages := &rmaInventoryStages{adapter: a, config: config, source: source}
	seen := map[string]bool{}
	hasCleanup := false
	for _, device := range inventory.devices {
		ref := device.reference
		serial, err := topologyidentity.CanonicalPhysicalIdentity(ref.Serial)
		if err != nil || seen[serial] || device.role != "" || device.roleSource != "" || len(device.tags) != 0 || device.site == "" || device.wireless != nil {
			return zero, errInvalidResponse
		}
		seen[serial] = true
		if want, present := actions[ref.Name]; present {
			if want.action != rmaMark {
				return zero, errInvalidResponse
			}
			stages.entries = append(stages.entries, &deviceReplacementMarkExecutor{adapter: a, config: config, source: source, reference: ref, desired: want})
		} else {
			if !retained[serial] {
				return zero, errConfigurationOwned
			}
			if _, _, err := rmaUnmarkPruneIntent(config, source, ref.Name); err != nil {
				return zero, err
			}
			hasCleanup = true
			stages.entries = append(stages.entries, &deviceReplacementUnmarkExecutor{adapter: a, config: config, source: source, name: ref.Name})
		}
	}
	for serial := range retained {
		if !seen[serial] {
			return zero, errConfigurationTransition
		}
	}
	plan := configurationApplyPlan{section: "inventory", executor: stages, observeCompletion: stages.observeCompletion, recordCompletion: stages.recordCompletion, sections: []configapi.NetworkControllerSectionStatus{{Name: "inventory", ResourceCount: int64(stages.count())}}}
	if hasCleanup {
		plan.finalize = stages.finalizeCleanup
	}
	return plan, nil
}
