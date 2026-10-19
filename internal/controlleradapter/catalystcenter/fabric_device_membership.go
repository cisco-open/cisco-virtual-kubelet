// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"sort"
)

const fabricDevicesPath = "/dna/intent/api/v1/sda/fabricDevices"

// Membership projection only. Border configuration is deliberately not a
// writable projection; fabric-device mutations require their own full model.
type fabricDeviceMembership struct {
	ID              string   `json:"id"`
	NetworkDeviceID string   `json:"networkDeviceId"`
	FabricID        string   `json:"fabricId"`
	Roles           []string `json:"deviceRoles"`
}

func (c *client) fabricDeviceMemberships(ctx context.Context, fabricID string) (map[string]fabricDeviceMembership, error) {
	if !validAPIID(fabricID) {
		return nil, errInvalidResponse
	}
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		return c.get(ctx, path, q, http.StatusOK)
	}
	rows, err := listInventoryQueryWithGet(ctx, get, fabricDevicesPath, url.Values{"fabricId": {fabricID}}, func(d fabricDeviceMembership) string { return d.ID })
	if err != nil {
		return nil, err
	}
	out := map[string]fabricDeviceMembership{}
	for _, row := range rows {
		if !validAPIID(row.ID) || !validAPIID(row.NetworkDeviceID) || row.FabricID != fabricID || len(row.Roles) == 0 {
			return nil, errInvalidResponse
		}
		if _, exists := out[row.NetworkDeviceID]; exists {
			return nil, errInvalidResponse
		}
		seen := map[string]bool{}
		for _, role := range row.Roles {
			switch role {
			case "CONTROL_PLANE_NODE", "EDGE_NODE", "BORDER_NODE", "EXTENDED_NODE", "WIRELESS_CONTROLLER_NODE":
			default:
				return nil, errInvalidResponse
			}
			if seen[role] {
				return nil, errInvalidResponse
			}
			seen[role] = true
		}
		sort.Strings(row.Roles)
		out[row.NetworkDeviceID] = row
	}
	return out, nil
}

// Callers must pass the complete RP device union for the site, including other
// VNs. This does not replace physical target admission or serial verification.
func validateFabricMulticastRPSet(fabricID string, ids []string, memberships map[string]fabricDeviceMembership, dualStack bool) error {
	if !validAPIID(fabricID) {
		return errInvalidResponse
	}
	selected := map[string]fabricDeviceMembership{}
	for _, id := range ids {
		if !validAPIID(id) {
			return errInvalidResponse
		}
		member, exists := memberships[id]
		if !exists {
			return errConfigurationBusy
		}
		if member.NetworkDeviceID != id || member.FabricID != fabricID || !validAPIID(member.ID) {
			return errConfigurationStale
		}
		if !slices.Contains(member.Roles, "BORDER_NODE") && !slices.Contains(member.Roles, "EDGE_NODE") {
			return errConfigurationTransition
		}
		selected[id] = member
	}
	if len(selected) > 2 || dualStack && len(selected) > 1 {
		return errConfigurationTransition
	}
	if len(selected) == 2 {
		for _, member := range selected {
			if !slices.Contains(member.Roles, "BORDER_NODE") {
				return errConfigurationTransition
			}
		}
	}
	return nil
}
