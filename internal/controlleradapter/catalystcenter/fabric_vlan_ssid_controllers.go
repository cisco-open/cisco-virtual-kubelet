// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"slices"
	"strconv"
)

type fabricSSIDControllerBinding struct {
	EmbeddedDigest                                                string `json:",omitempty"`
	Device                                                        Device
	Membership                                                    fabricDeviceMembership
	SiteID, SitePath, NetworkIntent, ModelConfig, LastProvisioned string
}

// This checks existing provisioning, not completion of a newly submitted task.
// Embedded WLCs require their own switchWirelessSetting readback contract.
func (c *client) fabricVLANSSIDControllers(ctx context.Context, fabricID string, members map[string]fabricDeviceMembership, inventory []Device, sites map[string]Site, modelRequired bool) ([]fabricSSIDControllerBinding, error) {
	return resolveFabricSSIDControllers(fabricID, members, inventory, sites, modelRequired, func(id string) (wirelessDeviceProvisionStatus, error) {
		return c.wirelessDeviceProvisionStatus(ctx, id)
	})
}

func resolveFabricSSIDControllers(fabricID string, members map[string]fabricDeviceMembership, inventory []Device, sites map[string]Site, modelRequired bool, status func(string) (wirelessDeviceProvisionStatus, error)) ([]fabricSSIDControllerBinding, error) {
	if !validAPIID(fabricID) || status == nil {
		return nil, errInvalidResponse
	}
	ids := []string{}
	for id, member := range members {
		if id != member.NetworkDeviceID || member.FabricID != fabricID || !validAPIID(member.ID) {
			return nil, errConfigurationStale
		}
		if slices.Contains(member.Roles, "WIRELESS_CONTROLLER_NODE") {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, errConfigurationBusy
	}
	physical, err := bindMulticastRPDevices(ids, inventory)
	if err != nil {
		return nil, err
	}
	out := make([]fabricSSIDControllerBinding, 0, len(physical))
	for _, device := range physical {
		if device.Family != "Wireless Controller" || device.Reachability != "Reachable" {
			return nil, errConfigurationBusy
		}
		current, err := status(device.ID)
		if err != nil {
			return nil, err
		}
		site, exists := sites[current.sitePath]
		if current.deviceID != device.ID || current.name != device.Hostname || !exists || !validAPIID(current.siteID) || site.ID != current.siteID || site.NameHierarchy != current.sitePath {
			return nil, errConfigurationStale
		}
		if current.outOfSync || current.networkIntent != "SUCCESS" || current.modelConfig != "SUCCESS" && (modelRequired || current.modelConfig != "NOT_STARTED") {
			return nil, errConfigurationBusy
		}
		stamp, err := strconv.ParseUint(current.lastProvisioned, 10, 64)
		if err != nil || stamp == 0 || strconv.FormatUint(stamp, 10) != current.lastProvisioned {
			return nil, errConfigurationBusy
		}
		out = append(out, fabricSSIDControllerBinding{Device: device, Membership: members[device.ID], SiteID: current.siteID, SitePath: current.sitePath, NetworkIntent: current.networkIntent, ModelConfig: current.modelConfig, LastProvisioned: current.lastProvisioned})
	}
	return out, nil
}
