// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
)

// A native scope is dependency evidence, not a mutation permit or proof that
// the target has the embedded wireless software package installed. Keep every
// fabric member because rolling AP settings are a site-wide concern.
type fabricEWLCScope struct {
	fabricID string
	members  []fabricDeviceMembership
	physical []Device
	settings []fabricEWLCSetting
	digest   string
}

func (c *client) fabricEWLCScope(ctx context.Context, fabricID string) (*fabricEWLCScope, error) {
	members, err := c.fabricDeviceMemberships(ctx, fabricID)
	if err != nil {
		return nil, err
	}
	settings, err := c.fabricEWLCSettings(ctx, fabricID)
	if err != nil {
		return nil, err
	}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	return resolveFabricEWLCScope(fabricID, members, inventory, settings)
}

func resolveFabricEWLCScope(fabricID string, memberships map[string]fabricDeviceMembership, inventory []Device, settings []fabricEWLCSetting) (*fabricEWLCScope, error) {
	if settings == nil || len(settings) > 2 {
		return nil, errInvalidResponse
	}
	members, physical, err := bindFabricMutationTargets(fabricID, memberships, inventory)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, errConfigurationBusy
	}
	for i := range members {
		members[i].Roles = slices.Clone(members[i].Roles)
		sort.Strings(members[i].Roles)
		if len(members[i].Roles) == 0 {
			return nil, errInvalidResponse
		}
		for j, role := range members[i].Roles {
			if j > 0 && role == members[i].Roles[j-1] {
				return nil, errInvalidResponse
			}
			switch role {
			case "EDGE_NODE", "BORDER_NODE", "CONTROL_PLANE_NODE", "EXTENDED_NODE", "WIRELESS_CONTROLLER_NODE":
			default:
				return nil, errInvalidResponse
			}
		}
	}
	out := &fabricEWLCScope{fabricID: fabricID, members: members, physical: physical, settings: slices.Clone(settings)}
	sort.Slice(out.settings, func(i, j int) bool { return out.settings[i].ID < out.settings[j].ID })
	for i, row := range out.settings {
		if !validAPIID(row.ID) || (i > 0 && out.settings[i-1].ID == row.ID) || (row.Rolling.Percentage != 5 && row.Rolling.Percentage != 15 && row.Rolling.Percentage != 25) {
			return nil, errInvalidResponse
		}
		if _, err := out.switchDevice(row.ID); err != nil {
			return nil, err
		}
	}
	raw, err := json.Marshal([]any{fabricID, out.members, out.physical, out.settings})
	if err != nil {
		return nil, err
	}
	out.digest = fmt.Sprintf("%x", sha256.Sum256(raw))
	return out, nil
}

func (scope *fabricEWLCScope) switchDevice(deviceID string) (Device, error) {
	if scope == nil || !validAPIID(deviceID) {
		return Device{}, errInvalidResponse
	}
	member := false
	for _, m := range scope.members {
		if m.NetworkDeviceID == deviceID {
			member = (slices.Contains(m.Roles, "EDGE_NODE") || slices.Contains(m.Roles, "BORDER_NODE")) && !slices.Contains(m.Roles, "WIRELESS_CONTROLLER_NODE") && !slices.Contains(m.Roles, "EXTENDED_NODE")
		}
	}
	if !member {
		return Device{}, errConfigurationBusy
	}
	for _, d := range scope.physical {
		if d.ID == deviceID && d.Family == "Switches and Hubs" && d.Reachability == "Reachable" {
			return d, nil
		}
	}
	return Device{}, errConfigurationBusy
}

// Select an explicitly identified switch without treating missing native rows
// as completed disablement. Enabling a third switch is refused before planning.
func (scope *fabricEWLCScope) target(deviceID string, want fabricEWLCSettings) (Device, *fabricEWLCSetting, error) {
	device, err := scope.switchDevice(deviceID)
	if err != nil {
		return Device{}, nil, err
	}
	if _, err := fabricEWLCPayload(deviceID, want); err != nil {
		return Device{}, nil, err
	}
	enabled := 0
	var observed *fabricEWLCSetting
	for _, row := range scope.settings {
		if row.Enabled {
			enabled++
		}
		if row.ID == deviceID {
			copy := row
			observed = &copy
		}
	}
	if want.enabled && (observed == nil || !observed.Enabled) && enabled >= 2 {
		return Device{}, nil, errConfigurationBusy
	}
	if !want.enabled && observed == nil {
		return Device{}, nil, errConfigurationBusy
	}
	return device, observed, nil
}
