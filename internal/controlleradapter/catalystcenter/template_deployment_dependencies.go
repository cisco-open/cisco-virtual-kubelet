// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

// Transient preflight evidence. Committed content can contain sensitive values;
// keep it in memory for revalidation, never journal it or hash it into a key.
// Parameter validation and mutation admission are separate.
type regularTemplateDependencies struct {
	device    *wiredProvisionSnapshot
	platform  templateDevicePlatform
	project   templateProject
	base      templateSummary
	version   templateVersion
	committed map[string]any
}

// Build transient request material only after dependency and parameter checks.
// This still does not confer mutation admission.
func (d *regularTemplateDependencies) payload(target regularTemplateTarget) ([]byte, error) {
	if d == nil || d.device == nil || !d.device.exists || d.project.Name != target.template.Project || d.base.Name != target.template.Name || d.base.ProjectID != d.project.ID || target.template.Onboarding || target.template.Composite || !validTemplateRedeploy(target.redeploy) {
		return nil, errConfigurationTransition
	}
	if err := validateTemplatePlatform(d.committed, d.platform); err != nil {
		return nil, err
	}
	if err := validateTemplateDeploymentParameters(d.committed, target.parameters); err != nil {
		return nil, err
	}
	return regularTemplateDeploymentPayload(regularTemplateDeployment{
		base: d.base, version: d.version, deviceID: d.device.target.ID,
		copyingConfig: target.copyingConfig, forcePush: target.forcePush,
		parameters: target.parameters,
	})
}

func (c *client) regularTemplateDependencies(ctx context.Context, device inventoryDeviceIntent, identity nacmodel.TemplateIdentity) (*regularTemplateDependencies, error) {
	return c.regularTemplateDependenciesAtVersion(ctx, device, identity, nil)
}

func (c *client) regularTemplateDependenciesAtVersion(ctx context.Context, device inventoryDeviceIntent, identity nacmodel.TemplateIdentity, pinned *templateDeploymentVersionBinding) (*regularTemplateDependencies, error) {
	if device.wireless != nil || identity.Onboarding || identity.Composite || !validProfileName(identity.Project) || !validProfileName(identity.Name) {
		return nil, errConfigurationTransition
	}
	physical, err := c.wiredProvisionSnapshot(ctx, device)
	if err != nil {
		return nil, err
	}
	if !physical.exists {
		return nil, errConfigurationBusy
	}
	content, err := c.regularTemplateContent(ctx, identity, pinned)
	if err != nil {
		return nil, err
	}
	platform, err := c.templateDevicePlatform(ctx, physical.target)
	if err != nil {
		return nil, err
	}
	if err := validateTemplatePlatform(content.body, platform); err != nil {
		return nil, err
	}
	return &regularTemplateDependencies{platform: platform, device: physical, project: content.project, base: content.base, version: content.version, committed: content.body}, nil
}

// A changed device incarnation, assignment/provision record, project/template
// UUID, latest version or committed content invalidates the original plan.
// Revalidation is observation only; it cannot authorize a native POST itself.
func (c *client) revalidateRegularTemplateDependencies(ctx context.Context, device inventoryDeviceIntent, identity nacmodel.TemplateIdentity, previous *regularTemplateDependencies) error {
	if previous == nil || previous.device == nil {
		return errConfigurationStale
	}
	current, err := c.regularTemplateDependencies(ctx, device, identity)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(previous, current) {
		return errConfigurationStale
	}
	return nil
}

type regularTemplateContent struct {
	project templateProject
	base    templateSummary
	version templateVersion
	body    map[string]any
}

func (c *client) regularTemplateContent(ctx context.Context, identity nacmodel.TemplateIdentity, pinned *templateDeploymentVersionBinding) (*regularTemplateContent, error) {
	if identity.Composite {
		return nil, errConfigurationTransition
	}
	return c.deploymentTemplateContent(ctx, identity, pinned)
}

// Shared native identity/version reader. Composite membership is validated by
// its caller; this reader requires the committed kind to match the intent.
func (c *client) deploymentTemplateContent(ctx context.Context, identity nacmodel.TemplateIdentity, pinned *templateDeploymentVersionBinding) (*regularTemplateContent, error) {
	if identity.Onboarding {
		return nil, errConfigurationTransition
	}
	return c.committedTemplateContent(ctx, identity, pinned)
}

// Shared committed-content lookup. Deployment keeps its Day-N-only gate above;
// PnP may inspect an onboarding definition without relaxing that gate.
func (c *client) committedTemplateContent(ctx context.Context, identity nacmodel.TemplateIdentity, pinned *templateDeploymentVersionBinding) (*regularTemplateContent, error) {
	if !validProfileName(identity.Project) || !validProfileName(identity.Name) {
		return nil, errConfigurationTransition
	}
	projects, err := c.listTemplateProjects(ctx)
	if err != nil {
		return nil, err
	}
	project, exists := projects[identity.Project]
	if !exists {
		return nil, errConfigurationBusy
	}
	definitions, err := c.listTemplateDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	base, exists := definitions[project.ID+"#"+identity.Name]
	if !exists {
		return nil, errConfigurationBusy
	}
	var version *templateVersion
	if pinned != nil {
		if base.ID != pinned.baseID || !validAPIID(pinned.version.ID) || pinned.version.Number < 1 {
			return nil, errConfigurationStale
		}
		copied := pinned.version
		version = &copied
	} else {
		version, err = c.latestTemplateVersion(ctx, base.ID)
		if err != nil {
			return nil, err
		}
	}
	if version == nil {
		return nil, errConfigurationBusy
	}
	body, err := c.readCommittedTemplate(ctx, base, *version)
	if err != nil {
		return nil, err
	}
	composite, ok := body["composite"].(bool)
	if !ok || composite != identity.Composite {
		return nil, errConfigurationTransition
	}
	return &regularTemplateContent{project: project, base: base, version: *version, body: body}, nil
}
