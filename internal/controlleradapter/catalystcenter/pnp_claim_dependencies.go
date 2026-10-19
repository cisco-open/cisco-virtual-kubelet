// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

// Ephemeral native dependencies. Template content may contain secrets and must
// never be journaled or emitted as drift. Identity/readiness/admission for the
// physical claim remain separate from this dependency observation.
type pnpClaimDependencies struct {
	refs     pnpClaimReferences
	site     Site
	image    *Image
	template *regularTemplateContent
	rf       *wirelessRFMetadata
}

func (pnpClaimDependencies) String() string   { return "<PnP claim dependencies>" }
func (pnpClaimDependencies) GoString() string { return "<PnP claim dependencies>" }

func (c *client) pnpClaimDependencies(ctx context.Context, want pnpDeviceIntent, deviceID string, index *nacmodel.TemplateIndex) (*pnpClaimDependencies, error) {
	if _, err := validatePnPDeviceIntent(want); err != nil {
		return nil, err
	}
	if !validAPIID(deviceID) {
		return nil, errInvalidResponse
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	result := &pnpClaimDependencies{refs: pnpClaimReferences{deviceID: deviceID}}
	for _, site := range sites {
		if site.NameHierarchy == want.Site {
			result.site = site
			result.refs.siteID = site.ID
		}
	}
	if result.refs.siteID == "" || result.site.Type == "global" {
		return nil, errConfigurationStale
	}
	// PnP AP claims require a floor, not merely an existing site. Repeating
	// dependency resolution also rechecks this constraint before dispatch.
	if want.Type == "AccessPoint" && result.site.Type != "floor" {
		return nil, errConfigurationTransition
	}
	if want.ImageID != "" || want.ImageName != "" {
		images, err := c.ListImages(ctx)
		if err != nil {
			return nil, err
		}
		for _, image := range images {
			name := image.Name
			if name == "" {
				name = image.ImageName
			}
			matches := want.ImageID != "" && image.ID == want.ImageID || want.ImageID == "" && name == want.ImageName
			if !matches {
				continue
			}
			if result.image != nil || !validAPIID(image.ID) {
				return nil, errInvalidResponse
			}
			copy := image
			result.image = &copy
			result.refs.imageID = image.ID
		}
		if result.image == nil {
			return nil, errConfigurationBusy
		}
	}
	if want.Template != nil {
		if index == nil {
			return nil, errConfigurationTransition
		}
		identity, _, err := index.ResolveReference(want.Template.Name)
		if err != nil {
			return nil, errConfigurationTransition
		}
		if identity.Composite {
			return nil, errConfigurationTransition
		}
		content, err := c.committedTemplateContent(ctx, identity, nil)
		if err != nil {
			return nil, err
		}
		parameters := map[string][]string{}
		for _, variable := range want.Template.Variables {
			parameters[variable.Name] = []string{variable.Value}
		}
		if err := validateTemplateDeploymentParameters(content.body, parameters); err != nil {
			return nil, err
		}
		result.template = content
		// The pinned PnP resource sends the base template ID. Retain the latest
		// committed version/body separately to detect changes before submission.
		result.refs.templateID = content.base.ID
	}
	if want.RFProfile != "" {
		profiles, err := c.wirelessRFProfiles(ctx)
		if err != nil {
			return nil, err
		}
		profile, ok := profiles[want.RFProfile]
		if !ok {
			return nil, errConfigurationBusy
		}
		result.rf = &profile
	}
	if _, err := pnpClaimPayload(want, result.refs); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *client) revalidatePnPClaimDependencies(ctx context.Context, want pnpDeviceIntent, deviceID string, index *nacmodel.TemplateIndex, previous *pnpClaimDependencies) error {
	if previous == nil {
		return errConfigurationStale
	}
	current, err := c.pnpClaimDependencies(ctx, want, deviceID, index)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(previous, current) {
		return errConfigurationStale
	}
	return nil
}
