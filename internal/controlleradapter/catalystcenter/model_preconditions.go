// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"errors"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

var errModelPrecondition = errors.New("pinned controller model precondition failed")

// Terraform's validation-only resources express model constraints, not appliance
// objects. Consume those constraints before routing executable source intent,
// reusing the existing parsers. This does not permit otherwise unsupported
// fields, AP execution or a new provisioning path. Resource-specific group
// preparation/recovery validates bounded operation models before this ordinary
// current-source preflight; this helper does not widen those group contracts.
func validatePinnedModelPreconditions(raw []byte) error {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		// Syntax/section support belongs to the existing compiler. This
		// preflight must not relabel an unparseable document as a model rule.
		return nil
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return nil
	}
	var profiles []accessPointProfile
	if value, present := root["wireless"]; present {
		wireless, ok := value.(map[string]any)
		if !ok {
			return errModelPrecondition
		}
		if value, present := wireless["access_point_configurations"]; present && value != nil {
			profiles, err = parseAccessPointProfiles(value)
			if err != nil {
				return errModelPrecondition
			}
		}
	}
	if value, present := root["inventory"]; present {
		inventory, ok := value.(map[string]any)
		if !ok {
			return errModelPrecondition
		}
		if value, present := inventory["devices"]; present && value != nil {
			devices, ok := value.([]any)
			if !ok || len(devices) > 10000 {
				return errModelPrecondition
			}
			for _, value := range devices {
				device, ok := value.(map[string]any)
				if !ok {
					return errModelPrecondition
				}
				if device["type"] == "AccessPoint" {
					if block, present := device["access_point"]; present && block != nil {
						serial, _ := device["serial_number"].(string)
						if _, _, err := parseAccessPointConfigurationReference(block, profiles, serial); err != nil {
							return errModelPrecondition
						}
					}
					continue
				}
				// Wireless facts are validated for every declared state, as upstream's
				// wireless_validation preconditions do. The ordinary compiler consumes
				// the original state; this projection cannot enable provisioning.
				fields := map[string]any{"state": "PROVISION"}
				relevant := device["type"] == "WirelessController"
				for _, key := range []string{"type", "fabric_site", "fabric_roles", "primary_managed_ap_locations", "secondary_managed_ap_locations", "anchor_managed_ap_locations"} {
					if value, present := device[key]; present && value != nil {
						fields[key] = value
					}
				}
				for _, key := range []string{"primary_managed_ap_locations", "secondary_managed_ap_locations", "anchor_managed_ap_locations"} {
					if fields[key] != nil {
						relevant = true
					}
				}
				if roles, ok := fields["fabric_roles"].([]any); ok {
					for _, role := range roles {
						if role == "WIRELESS_CONTROLLER_NODE" || role == "EMBEDDED_WIRELESS_CONTROLLER_NODE" {
							relevant = true
						}
					}
				}
				if relevant {
					if _, err := parseWirelessDevicePlacement(fields); err != nil {
						return errModelPrecondition
					}
				}
			}
		}
	}
	return nil
}
