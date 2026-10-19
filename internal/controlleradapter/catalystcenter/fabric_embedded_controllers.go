// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"strings"
)

// Bind enabled embedded controllers using wired provisioning and native wireless
// settings, not the standalone WLC provisioning endpoint. Every observation is
// retained as dependency evidence; this grants no mutation authority.
func (c *client) fabricEmbeddedSSIDControllers(ctx context.Context, path, fabricID string, members map[string]fabricDeviceMembership, inventory []Device, sites map[string]Site) ([]fabricSSIDControllerBinding, error) {
	settings, err := c.fabricEWLCSettings(ctx, fabricID)
	if err != nil {
		return nil, err
	}
	enabled := false
	for _, row := range settings {
		enabled = enabled || row.Enabled
	}
	if !enabled {
		return nil, errConfigurationBusy
	}
	if inventory == nil {
		inventory, err = c.configurationDevices(ctx)
		if err != nil {
			return nil, err
		}
	}
	if sites == nil {
		rows, err := c.ListSites(ctx)
		if err != nil {
			return nil, err
		}
		sites = map[string]Site{}
		for _, row := range rows {
			sites[row.NameHierarchy] = row
		}
	}
	scope, err := resolveFabricEWLCScope(fabricID, members, inventory, settings)
	if err != nil {
		return nil, err
	}
	out := []fabricSSIDControllerBinding{}
	for _, row := range scope.settings {
		if !row.Enabled {
			continue
		}
		device, err := scope.switchDevice(row.ID)
		if err != nil {
			return nil, err
		}
		provisioned, ready, err := c.wiredProvisionedDevice(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		if !ready {
			return nil, errConfigurationBusy
		}
		sitePath := ""
		for candidate, site := range sites {
			if site.ID == provisioned.SiteID {
				if sitePath != "" || site.NameHierarchy != candidate {
					return nil, errConfigurationStale
				}
				sitePath = candidate
			}
		}
		if sitePath == "" || sitePath != path && !strings.HasPrefix(sitePath, path+"/") {
			return nil, errConfigurationStale
		}
		want := fabricEWLCIntent{device: fabricDeviceIntent{inventory: inventoryDeviceIntent{reference: configurationDeviceReference{Name: device.Hostname, Serial: device.Serial}, site: sitePath}, state: "PROVISION", fabricSite: path}, settings: fabricEWLCSettings{enabled: true, rolling: row.Rolling.Enabled, percentage: row.Rolling.Percentage}}
		deps, err := c.fabricEWLCFabricDependencies(ctx, want)
		if err != nil {
			return nil, err
		}
		if deps.target.ID != row.ID || deps.scope.digest != scope.digest || deps.observed == nil || *deps.observed != row {
			return nil, errConfigurationStale
		}
		out = append(out, fabricSSIDControllerBinding{Device: deps.target, Membership: members[row.ID], SiteID: provisioned.SiteID, SitePath: sitePath, EmbeddedDigest: deps.digest})
	}
	return out, nil
}
