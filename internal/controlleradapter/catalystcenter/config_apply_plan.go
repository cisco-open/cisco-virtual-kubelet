// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"slices"
	"time"
)

type configurationApplyPlan struct {
	// Optional owned bookkeeping after all native effects converge. It cannot
	// authorize another native mutation or release a device reservation.
	finalize func(context.Context) error
	// Optional resource-specific retained-task recovery. It cannot authorize
	// submission; the common loop still requires unchanged owner/source bindings.
	recoverKnownTask func(*cvk.NetworkControllerConfigurationOperation) bool
	// Optional receipt observer; nil preserves generic task completion.
	observeCompletion func(context.Context, *cvk.NetworkControllerConfigurationOperation, time.Time) (bool, error)
	// Optional atomic verification/receipt writer; nil preserves existing verification.
	recordCompletion func(context.Context, *cvk.NetworkControllerConfigurationOperation) error
	// Optional selector for mixed plans; nil preserves the all-operation contract.
	receiptOperation func(*cvk.NetworkControllerConfigurationOperation) bool
	executor         configurationExecutor
	section          string
	sections         []configapi.NetworkControllerSectionStatus
}

func (p configurationApplyPlan) statuses(state configapi.NetworkControllerSectionState) []configapi.NetworkControllerSectionStatus {
	out := append([]configapi.NetworkControllerSectionStatus{}, p.sections...)
	for i := range out {
		out[i].State = state
	}
	return out
}
func combinedTagSections(sections []string) bool {
	return len(sections) == 2 && slices.Contains(sections, "templates") && slices.Contains(sections, "inventory")
}
func (c *client) compileConfigurationApply(raw []byte, sections []string, files map[string]string) (configurationApplyPlan, error) {
	var plan configurationApplyPlan
	if combinedFabricIPAMSections(sections) {
		return c.compileFabricIPAMApply(raw, sections)
	}
	if len(sections) == 1 {
		executor, err := c.compileConfigurationExecutor(raw, sections[0], files)
		if err != nil {
			return plan, err
		}
		plan = configurationApplyPlan{executor: executor, section: sections[0], sections: []configapi.NetworkControllerSectionStatus{{Name: sections[0], ResourceCount: int64(executor.count())}}}
		if sections[0] == "feature_templates" {
			plan.recoverKnownTask = wirelessFeatureKnownTaskRecovery(executor)
		} else if sections[0] == "wireless" {
			plan.recoverKnownTask = wirelessDefinitionKnownTaskRecovery(executor)
			plan.observeCompletion = c.wirelessDefinitionTaskCompletion(executor)
		} else if sections[0] == "templates" {
			plan.recoverKnownTask = templateKnownTaskRecovery(executor)
			plan.observeCompletion = c.templateTaskCompletion(executor)
		}
		if sections[0] == "network_settings" {
			bindSiteCredentialTaskRecovery(&plan)
		}
		c.bindTagTaskRecovery(&plan)
		return plan, nil
	}
	if !combinedTagSections(sections) {
		return plan, errors.New("unsupported apply section combination")
	}
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return plan, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 2 {
		return plan, errors.New("combined apply requires only inventory and templates")
	}
	templates, ok := root["templates"].(map[string]any)
	if !ok {
		return plan, errors.New("invalid template section")
	}
	inventoryRaw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": root["inventory"]}})
	inventory, err := parseInventoryIntent(inventoryRaw)
	if err != nil {
		return plan, err
	}
	if len(inventory.devices) > 0 {
		return plan, errors.New("combined inventory device execution is not yet supported")
	}
	// Validate shadowed entries too; unsupported intent must not disappear.
	if tags, present := templates["tags"]; present {
		if _, err := parseTemplateTags(tags); err != nil {
			return plan, err
		}
	}
	if _, err := normalizeReportTags(root); err != nil {
		return plan, err
	}
	merged := []any{}
	if tags, present := templates["tags"]; present {
		merged = append(merged, tags.([]any)...)
	}
	inv := root["inventory"].(map[string]any)
	if tags, present := inv["tags"]; present {
		merged = append(merged, tags.([]any)...)
	}
	_, templateTagSelection := templates["tags"]
	_, inventoryTagSelection := inv["tags"]
	if len(merged) > 0 || templateTagSelection || inventoryTagSelection {
		templates["tags"] = merged
	}
	canonical, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"templates": templates}})
	executor, err := c.compileConfigurationExecutor(canonical, "templates", files)
	if err != nil {
		return plan, err
	}
	// The canonical templates journal namespace is stable across section order;
	// both selected sections are reserved atomically by the same config owner.
	plan.executor, plan.section = executor, "templates"
	if len(inventory.discoveries) > 0 {
		plan.executor = &configurationGroup{parts: []configurationPart{
			{name: "templates", executor: executor},
			{name: "discoveries", executor: &discoveryExecutor{client: c, desired: inventory.discoveries}},
		}}
	}
	for _, section := range sections {
		count := len(inventory.tags) + len(inventory.discoveries)
		if section == "templates" {
			count = executor.count() - len(inventory.tags)
		}
		plan.sections = append(plan.sections, configapi.NetworkControllerSectionStatus{Name: section, ResourceCount: int64(count)})
	}
	c.bindTagTaskRecovery(&plan)
	return plan, nil
}
