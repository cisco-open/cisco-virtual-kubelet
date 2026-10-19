// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

// Only nonsensitive UUID/version bindings belong in recovery evidence. Native
// content remains transient, just as for regular-template preflight.
type compositeDeploymentVersionBinding struct {
	parent  templateDeploymentVersionBinding
	members []templateDeploymentVersionBinding
}
type compositeDeploymentContent struct {
	parent  *regularTemplateContent
	members []*regularTemplateContent
}

func (c *client) compositeTemplateContent(ctx context.Context, identity nacmodel.TemplateIdentity, pinned *compositeDeploymentVersionBinding) (*compositeDeploymentContent, error) {
	if !identity.Composite || identity.Onboarding {
		return nil, errConfigurationTransition
	}
	var parentBinding *templateDeploymentVersionBinding
	if pinned != nil {
		parentBinding = &pinned.parent
	}
	parent, err := c.deploymentTemplateContent(ctx, identity, parentBinding)
	if err != nil {
		return nil, err
	}
	rows, ok := parent.body["containingTemplates"].([]any)
	if !ok || len(rows) == 0 || len(rows) > 1000 {
		return nil, errInvalidResponse
	}
	if len(identity.Members) > 0 && len(identity.Members) != len(rows) {
		return nil, errConfigurationStale
	}
	if pinned != nil && len(pinned.members) != len(rows) {
		return nil, errConfigurationStale
	}
	// Resolve native base UUIDs against the independently listed definitions.
	// Member project/name metadata is never sufficient to substitute an identity.
	projects, err := c.listTemplateProjects(ctx)
	if err != nil {
		return nil, err
	}
	definitions, err := c.listTemplateDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]templateSummary{}
	for _, definition := range definitions {
		byID[definition.ID] = definition
	}
	projectNames := map[string]string{}
	for name, project := range projects {
		projectNames[project.ID] = name
	}
	out := &compositeDeploymentContent{parent: parent}
	seenIDs := map[string]bool{parent.base.ID: true, parent.version.ID: true}
	seenNames := map[string]bool{}
	for i, row := range rows {
		member, ok := row.(map[string]any)
		if !ok {
			return nil, errInvalidResponse
		}
		id, ok := member["id"].(string)
		if !ok || !validAPIID(id) || seenIDs[id] {
			return nil, errInvalidResponse
		}
		base, exists := byID[id]
		if !exists {
			return nil, errConfigurationBusy
		}
		project := projectNames[base.ProjectID]
		if project == "" || seenNames[base.Name] {
			return nil, errConfigurationTransition
		}
		for key, expected := range map[string]string{"name": base.Name, "projectName": project, "projectId": base.ProjectID} {
			if value, present := member[key]; present && value != expected {
				return nil, errConfigurationStale
			}
		}
		if len(identity.Members) > 0 && identity.Members[i] != project+"#"+base.Name {
			return nil, errConfigurationStale
		}
		var binding *templateDeploymentVersionBinding
		if pinned != nil {
			binding = &pinned.members[i]
			if binding.baseID != id {
				return nil, errConfigurationStale
			}
		}
		content, err := c.regularTemplateContent(ctx, nacmodel.TemplateIdentity{Project: project, Name: base.Name}, binding)
		if err != nil {
			return nil, err
		}
		if content.base != base || seenIDs[content.version.ID] {
			return nil, errConfigurationStale
		}
		seenIDs[id], seenIDs[content.version.ID], seenNames[base.Name] = true, true, true
		out.members = append(out.members, content)
	}
	return out, nil
}

// Fresh planning must fail if membership, order, any committed version or body
// changed since preflight. Recovery instead supplies explicit pinned bindings.
func (c *client) revalidateCompositeTemplateContent(ctx context.Context, identity nacmodel.TemplateIdentity, previous *compositeDeploymentContent) error {
	if previous == nil || previous.parent == nil || len(previous.members) == 0 {
		return errConfigurationStale
	}
	current, err := c.compositeTemplateContent(ctx, identity, nil)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, previous) {
		return errConfigurationStale
	}
	return nil
}

// Combine the bound graph and per-member variables only after validating every
// member against the same physical platform. Unknown member names cannot be
// silently dropped; sensitive variables remain transient request material.
func (d *compositeDeploymentContent) payload(platform templateDevicePlatform, copyingConfig, forcePush bool, parameters map[string]map[string][]string) ([]byte, error) {
	if d == nil || d.parent == nil || len(d.members) == 0 || !validAPIID(platform.ID) {
		return nil, errConfigurationTransition
	}
	if err := validateTemplatePlatform(d.parent.body, platform); err != nil {
		return nil, err
	}
	want := compositeTemplateDeployment{base: d.parent.base, version: d.parent.version, deviceID: platform.ID, forcePush: forcePush}
	known := map[string]bool{}
	for _, member := range d.members {
		if member == nil || known[member.base.Name] {
			return nil, errConfigurationStale
		}
		known[member.base.Name] = true
		if err := validateTemplatePlatform(member.body, platform); err != nil {
			return nil, err
		}
		values := parameters[member.base.Name]
		if err := validateTemplateDeploymentParameters(member.body, values); err != nil {
			return nil, err
		}
		want.members = append(want.members, regularTemplateDeployment{base: member.base, version: member.version, deviceID: platform.ID, copyingConfig: copyingConfig, forcePush: forcePush, parameters: values})
	}
	for name := range parameters {
		if !known[name] {
			return nil, errConfigurationTransition
		}
	}
	return compositeTemplateDeploymentPayload(want)
}
