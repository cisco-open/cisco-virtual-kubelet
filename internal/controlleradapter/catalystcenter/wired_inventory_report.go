// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"reflect"
)

// Report describes observation gaps, not a dispatchable mutation plan. In
// particular it cannot turn a REPROVISION action into a readback-only success.
func (c *client) reportWiredInventoryIntent(ctx context.Context, intent inventoryIntent, states map[string]string, outcomes *discoveryOutcomeSummary) ([]configapi.NetworkControllerDriftEntry, error) {
	before, err := c.wiredInventoryBindings(ctx, intent, states)
	if err != nil {
		return nil, err
	}
	out, err := c.reportInventoryIntent(ctx, intent, outcomes)
	if err != nil {
		return nil, err
	}
	for _, want := range intent.devices {
		state := states[want.reference.Name]
		if state == "" {
			continue
		}
		if state != "PROVISION" && state != "REPROVISION" {
			return nil, errInvalidResponse
		}
		binding, exists := before[want.reference.Name]
		if !exists {
			return nil, errConfigurationStale
		}
		got, present, err := c.wiredProvisionedDevice(ctx, binding.deviceID)
		if err != nil {
			return nil, err
		}
		path := "/provisioning"
		change := configapi.NetworkControllerDriftChangeAdd
		switch {
		case !present:
		case got.SiteID != binding.siteID:
			path = "/provisioning/site"
			change = configapi.NetworkControllerDriftChangeUpdate
		case state == "REPROVISION":
			path = "/reprovision"
			change = configapi.NetworkControllerDriftChangeUpdate
		default:
			continue
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "inventory", Resource: want.reference.Name, Path: path, Change: change})
	}
	after, err := c.wiredInventoryBindings(ctx, intent, states)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(before, after) {
		return nil, errConfigurationStale
	}
	return out, nil
}
