// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

type fabricEWLCIntent struct {
	device    fabricDeviceIntent
	settings  fabricEWLCSettings
	locations *wirelessManagedAPIntent
}

// Validate embedded-WLC intent as an extension of the existing fabric-device
// projection. The caller separately establishes prerequisites and execution authority.
func parseFabricEWLCIntents(raw []byte) ([]fabricEWLCIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return nil, errInvalidResponse
	}
	inventory, ok := root["inventory"].(map[string]any)
	if !ok || len(inventory) != 1 {
		return nil, errInvalidResponse
	}
	rows, ok := inventory["devices"].([]any)
	if !ok || len(rows) == 0 || len(rows) > 1000 {
		return nil, errInvalidResponse
	}
	settings := map[string]fabricEWLCSettings{}
	locations := map[string]*wirelessManagedAPIntent{}
	for _, row := range rows {
		model, ok := row.(map[string]any)
		if !ok {
			return nil, errInvalidResponse
		}
		name, ok := model["name"].(string)
		if !ok || name == "" {
			return nil, errInvalidResponse
		}
		if _, exists := settings[name]; exists {
			return nil, errInvalidResponse
		}
		fields := map[string]any{}
		for _, key := range []string{"enable_wireless", "enable_rolling_ap_upgrade", "ap_reboot_percentage"} {
			if value, present := model[key]; present {
				fields[key] = value
				delete(model, key)
			}
		}
		parsed, err := parseFabricEWLCSettings(fields)
		if err != nil {
			return nil, err
		}
		settings[name] = parsed
		roles, ok := model["fabric_roles"].([]any)
		if !ok {
			return nil, errInvalidResponse
		}
		base := []any{}
		embedded := 0
		for _, role := range roles {
			text, ok := role.(string)
			if !ok {
				return nil, errInvalidResponse
			}
			if text == "EMBEDDED_WIRELESS_CONTROLLER_NODE" {
				embedded++
			} else {
				base = append(base, role)
			}
		}
		if embedded != 1 {
			return nil, errInvalidResponse
		}
		placement := map[string]any{}
		hasLocations := false
		for _, key := range []string{"state", "type", "fabric_site", "fabric_roles", "primary_managed_ap_locations", "secondary_managed_ap_locations", "anchor_managed_ap_locations"} {
			if value, present := model[key]; present {
				placement[key] = value
				if key == "primary_managed_ap_locations" || key == "secondary_managed_ap_locations" || key == "anchor_managed_ap_locations" {
					hasLocations = true
					delete(model, key)
				}
			}
		}
		if hasLocations {
			p, err := parseWirelessDevicePlacement(placement)
			if err != nil {
				return nil, err
			}
			locations[name] = &p.locations
		}
		model["fabric_roles"] = base
	}
	projected, err := json.Marshal(map[string]any{"catalyst_center": root})
	if err != nil {
		return nil, err
	}
	devices, err := parseFabricDeviceIntentsWithExistingRoles(projected, true)
	if err != nil {
		return nil, err
	}
	out := make([]fabricEWLCIntent, 0, len(devices))
	for _, device := range devices {
		name := device.inventory.reference.Name
		out = append(out, fabricEWLCIntent{device: device, settings: settings[name], locations: locations[name]})
	}
	return out, nil
}
