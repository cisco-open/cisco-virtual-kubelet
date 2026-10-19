// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "encoding/json"

type accessPointDeviceIntent struct {
	inventory        inventoryDeviceIntent
	state, rfProfile string
	profile          *accessPointProfile
	manageHostname   bool
}

// Validate AP-specific intent independently, then reuse inventory validation for
// identity, site, tags and device role. This does not enable APs in the generic
// inventory compiler or discard their provisioning/configuration actions.
func parseAccessPointDevice(model map[string]any, profiles []accessPointProfile) (accessPointDeviceIntent, error) {
	var out accessPointDeviceIntent
	if model["type"] != "AccessPoint" {
		return out, errAccessPointModel
	}
	state, ok := model["state"].(string)
	if !ok || state != "ASSIGN" && state != "PROVISION" && state != "REPROVISION" {
		return out, errAccessPointModel
	}
	out.state = state
	base := map[string]any{}
	for key, value := range model {
		switch key {
		case "type", "state", "access_point", "rf_profile":
		default:
			base[key] = value
		}
	}
	base["state"] = "ASSIGN"
	raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": []any{base}}}})
	if err != nil {
		return out, errAccessPointModel
	}
	inventory, err := parseInventoryIntent(raw)
	if err != nil || len(inventory.devices) != 1 {
		return out, errAccessPointModel
	}
	out.inventory = inventory.devices[0]
	if value, present := model["rf_profile"]; present && value != nil {
		out.rfProfile, err = plannedAPText(value)
		if err != nil {
			return accessPointDeviceIntent{}, errAccessPointModel
		}
	}
	if value, present := model["access_point"]; present && value != nil {
		out.profile, out.manageHostname, err = parseAccessPointConfigurationReference(value, profiles, out.inventory.reference.Serial)
		if err != nil {
			return accessPointDeviceIntent{}, err
		}
	}
	// An executable CVK plan must not report convergence for settings that the
	// module would only warn are ignored in ASSIGN state.
	if state == "ASSIGN" && (out.profile != nil || out.manageHostname || out.rfProfile != "") {
		return accessPointDeviceIntent{}, errAccessPointModel
	}
	if state != "ASSIGN" && out.rfProfile == "" {
		return accessPointDeviceIntent{}, errAccessPointModel
	}
	return out, nil
}

// Reuse the pinned reference/rename preconditions independently of deployment
// state. A declared ASSIGN block must still name a real profile and stable serial
// for rename; executable state and physical admission remain separate checks.
func parseAccessPointConfigurationReference(value any, profiles []accessPointProfile, serial string) (*accessPointProfile, bool, error) {
	block, ok := value.(map[string]any)
	if !ok {
		return nil, false, errAccessPointModel
	}
	var profile *accessPointProfile
	manage := false
	for key, value := range block {
		switch key {
		case "configuration":
			if value == nil {
				continue
			}
			name, err := plannedAPText(value)
			if err != nil {
				return nil, false, errAccessPointModel
			}
			for i := range profiles {
				if profiles[i].name == name {
					if profile != nil {
						return nil, false, errAccessPointModel
					}
					copy := profiles[i]
					profile = &copy
				}
			}
			if profile == nil {
				return nil, false, errAccessPointModel
			}
		case "manage_hostname":
			if value == nil {
				continue
			}
			enabled, ok := value.(bool)
			if !ok {
				return nil, false, errAccessPointModel
			}
			manage = enabled
		default:
			return nil, false, errAccessPointModel
		}
	}
	if manage && serial == "" {
		return nil, false, errAccessPointModel
	}
	return profile, manage, nil
}
