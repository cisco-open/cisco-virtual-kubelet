// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"sort"
)

const pnpWorkflowsPath = "/dna/intent/api/v1/onboarding/pnp-workflow"

type templatePnPReference struct{ DeviceID, WorkflowID, Kind, ConfigID string }

func pnpWorkflowConfigIDs(workflow *pnpWorkflowIdentity) []string {
	seen := map[string]bool{}
	if workflow.ConfigID != "" {
		seen[workflow.ConfigID] = true
	}
	for _, task := range workflow.Tasks {
		if task.ConfigInfo != nil && task.ConfigInfo.ConfigID != "" {
			seen[task.ConfigInfo.ConfigID] = true
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (c *client) pnpWorkflows(ctx context.Context) ([]pnpWorkflowIdentity, error) {
	before, err := c.pnpCollectionCount(ctx, pnpWorkflowsPath)
	if err != nil {
		return nil, err
	}
	rows, err := listPnPPages(ctx, c, pnpWorkflowsPath, func(w pnpWorkflowIdentity) string { return w.ID }, 500)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if !validAPIID(rows[i].ID) {
			return nil, errInvalidResponse
		}
		if err := validatePnPWorkflowMetadata(&rows[i]); err != nil {
			return nil, err
		}
	}
	after, err := c.pnpCollectionCount(ctx, pnpWorkflowsPath)
	if err != nil {
		return nil, err
	}
	if before != after || len(rows) != before {
		return nil, errConfigurationStale
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}
func (c *client) templateReferenceAliases(ctx context.Context, target templateSummary) (map[string]bool, error) {
	if !validAPIID(target.ID) || !validAPIID(target.ProjectID) || target.Name == "" {
		return nil, errInvalidResponse
	}
	if _, err := c.readTemplateDefinition(ctx, target); err != nil {
		return nil, err
	}
	footprint, err := c.templateVersionFootprint(ctx, target)
	if err != nil {
		return nil, err
	}
	entries, err := templateVersionEntries(footprint, target.ID)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{target.ID: true}
	for id := range entries {
		out[id] = true
	}
	return out, nil
}

// Include unused definitions as well as main/system/reset execution instances.
// Completed states do not erase a reference or grant template-removal authority.
// The existing projection excludes names, commands, outputs and credentials.
func (c *client) templatePnPReferences(ctx context.Context, target templateSummary) ([]templatePnPReference, error) {
	aliases, err := c.templateReferenceAliases(ctx, target)
	if err != nil {
		return nil, err
	}
	workflows, err := c.pnpWorkflows(ctx)
	if err != nil {
		return nil, err
	}
	out := []templatePnPReference{}
	library := map[string]pnpWorkflowIdentity{}
	for _, workflow := range workflows {
		library[workflow.ID] = workflow
		for _, configID := range pnpWorkflowConfigIDs(&workflow) {
			if aliases[configID] {
				out = append(out, templatePnPReference{WorkflowID: workflow.ID, Kind: "definition", ConfigID: configID})
			}
		}
	}
	before, err := c.pnpCollectionCount(ctx, pnpDevicesPath)
	if err != nil {
		return nil, err
	}
	devices, err := c.pnpDevices(ctx)
	if err != nil {
		return nil, err
	}
	after, err := c.pnpCollectionCount(ctx, pnpDevicesPath)
	if err != nil {
		return nil, err
	}
	if before != after || len(devices) != before {
		return nil, errConfigurationStale
	}
	for _, device := range devices {
		if device.Info.WorkflowID != "" {
			if _, exists := library[device.Info.WorkflowID]; !exists {
				return nil, errInvalidResponse
			}
		}
		if device.Parameters != nil {
			if len(device.Parameters.ConfigList) > 1000 {
				return nil, errInvalidResponse
			}
			seen := map[string]bool{}
			for _, config := range device.Parameters.ConfigList {
				if !validAPIID(config.ConfigID) {
					return nil, errInvalidResponse
				}
				if aliases[config.ConfigID] && !seen[config.ConfigID] {
					out = append(out, templatePnPReference{DeviceID: device.ID, Kind: "workflowParameters", ConfigID: config.ConfigID})
				}
				seen[config.ConfigID] = true
			}
		}
		for _, entry := range []struct {
			kind     string
			workflow *pnpWorkflowIdentity
		}{{"workflow", device.Workflow}, {"systemWorkflow", device.SystemWorkflow}, {"systemResetWorkflow", device.ResetWorkflow}} {
			if entry.workflow == nil {
				continue
			}
			workflow := entry.workflow
			for _, configID := range pnpWorkflowConfigIDs(workflow) {
				if aliases[configID] {
					id := workflow.ID
					if id == "" && entry.kind == "workflow" {
						id = device.Info.WorkflowID
					}
					if !validAPIID(id) {
						return nil, errInvalidResponse
					}
					out = append(out, templatePnPReference{DeviceID: device.ID, WorkflowID: id, Kind: entry.kind, ConfigID: configID})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.DeviceID != b.DeviceID {
			return a.DeviceID < b.DeviceID
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.WorkflowID != b.WorkflowID {
			return a.WorkflowID < b.WorkflowID
		}
		return a.ConfigID < b.ConfigID
	})
	return out, nil
}
func (c *client) stableTemplatePnPReferences(ctx context.Context, target templateSummary) ([]templatePnPReference, error) {
	first, err := c.templatePnPReferences(ctx, target)
	if err != nil {
		return nil, err
	}
	second, err := c.templatePnPReferences(ctx, target)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(first, second) {
		return nil, errConfigurationStale
	}
	return first, nil
}
