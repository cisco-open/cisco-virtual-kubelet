// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
)

// Only nonsensitive identity/options enter the resource key. Version UUID and
// number remain explicit so recovery can later select the submitted version,
// rather than assuming the latest committed version is still the same.
func regularTemplateDeploymentResource(snapshot *regularTemplateDependencies, target regularTemplateTarget) (string, error) {
	raw, _ := json.Marshal(struct {
		Physical       string
		Provision      wiredProvisionedDevice
		Project        string
		Platform       templateDevicePlatform
		Copying, Force bool
	}{snapshot.device.digest, snapshot.device.before, snapshot.project.ID, snapshot.platform, target.copyingConfig, target.forcePush})
	resource := fmt.Sprintf("template-deploy:%s/%s/%d/%x", snapshot.base.ID, snapshot.version.ID, snapshot.version.Number, sha256.Sum256(raw))
	if target.comparison == nil {
		return resource, nil
	}
	payload, err := snapshot.payload(target)
	if err != nil {
		return "", err
	}
	return target.comparison.bind(resource, payload)
}

// Native single-device planning only. The adapter must bind source/owner,
// durable physical admission and redeploy receipts before invoking submit.
// The inventory compiler reaches this through the owned adapter planner.
func (c *client) planRegularTemplateDeployment(ctx context.Context, device inventoryDeviceIntent, target regularTemplateTarget) (*configurationWrite, error) {
	snapshot, err := c.regularTemplateDependencies(ctx, device, target.template)
	if err != nil {
		return nil, err
	}
	payload, err := snapshot.payload(target)
	if err != nil {
		return nil, err
	}
	resource, err := regularTemplateDeploymentResource(snapshot, target)
	if err != nil {
		return nil, err
	}
	if len(resource) > 512 {
		return nil, errInvalidResponse
	}
	// payload owns its bytes; later changes to the caller's parameter map cannot
	// change the native effect after planning. Dependencies are independently read.
	return &configurationWrite{
		action: "Update", resource: resource,
		identity: configurationResourceIdentity{target: snapshot.device.target.ID, parent: snapshot.device.site.ID, serial: snapshot.device.target.Serial},
		submit: func(ctx context.Context) (Task, error) {
			if err := c.revalidateRegularTemplateDependencies(ctx, device, target.template, snapshot); err != nil {
				return Task{}, err
			}
			if target.comparison != nil {
				if target.comparison.check == nil {
					return Task{}, errConfigurationSecret
				}
				if err := target.comparison.check(ctx); err != nil {
					return Task{}, err
				}
			}
			raw, err := c.mutate(ctx, http.MethodPost, templateDeployPath, payload)
			if err != nil {
				return Task{}, err
			}
			id, err := templateDeploymentReceipt(raw)
			if err != nil {
				return Task{}, err
			}
			return Task{ID: id}, nil
		},
	}, nil
}
