// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

// Transient composite preflight; physical and native content evidence use the
// same readers as regular deployment. This is not a mutation permit.
type compositeTemplateDependencies struct {
	device   *wiredProvisionSnapshot
	platform templateDevicePlatform
	content  *compositeDeploymentContent
}

func (c *client) compositeTemplateDependenciesAtVersion(ctx context.Context, device inventoryDeviceIntent, identity nacmodel.TemplateIdentity, pinned *compositeDeploymentVersionBinding) (*compositeTemplateDependencies, error) {
	if device.wireless != nil || !identity.Composite || identity.Onboarding {
		return nil, errConfigurationTransition
	}
	physical, err := c.wiredProvisionSnapshot(ctx, device)
	if err != nil {
		return nil, err
	}
	if !physical.exists {
		return nil, errConfigurationBusy
	}
	content, err := c.compositeTemplateContent(ctx, identity, pinned)
	if err != nil {
		return nil, err
	}
	platform, err := c.templateDevicePlatform(ctx, physical.target)
	if err != nil {
		return nil, err
	}
	if err := validateTemplatePlatform(content.parent.body, platform); err != nil {
		return nil, err
	}
	for _, member := range content.members {
		if err := validateTemplatePlatform(member.body, platform); err != nil {
			return nil, err
		}
	}
	return &compositeTemplateDependencies{device: physical, platform: platform, content: content}, nil
}

func (d *compositeTemplateDependencies) payload(copyingConfig, forcePush bool, parameters map[string]map[string][]string) ([]byte, error) {
	if d == nil || d.device == nil || !d.device.exists || d.content == nil || d.platform.ID != d.device.target.ID || d.platform.Serial != d.device.target.Serial {
		return nil, errConfigurationStale
	}
	return d.content.payload(d.platform, copyingConfig, forcePush, parameters)
}

func (c *client) revalidateCompositeTemplateDependencies(ctx context.Context, device inventoryDeviceIntent, identity nacmodel.TemplateIdentity, previous *compositeTemplateDependencies) error {
	if previous == nil || previous.device == nil || previous.content == nil {
		return errConfigurationStale
	}
	current, err := c.compositeTemplateDependenciesAtVersion(ctx, device, identity, nil)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(previous, current) {
		return errConfigurationStale
	}
	return nil
}
