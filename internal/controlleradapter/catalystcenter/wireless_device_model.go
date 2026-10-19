// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"errors"
	"sort"
	"strings"
)

var errWirelessDeviceModel = errors.New("invalid wireless device placement model")

type wirelessDevicePlacement struct {
	state, deviceType, fabricSite string
	roles                         []string
	locations                     wirelessManagedAPIntent
}

// Parse a section-specific projection of an inventory device. The enclosing
// inventory compiler must separately validate identity and all remaining fields;
// this helper never silently accepts a whole device with unrecognized fields.
func parseWirelessDevicePlacement(fields map[string]any) (wirelessDevicePlacement, error) {
	var out wirelessDevicePlacement
	for key, value := range fields {
		switch key {
		case "state", "type", "fabric_site":
			s, ok := value.(string)
			if !ok || s == "" || strings.TrimSpace(s) != s || strings.ContainsAny(s, "\x00\r\n") {
				return out, errWirelessDeviceModel
			}
			switch key {
			case "state":
				out.state = s
			case "type":
				out.deviceType = s
			case "fabric_site":
				out.fabricSite = s
			}
		case "primary_managed_ap_locations", "secondary_managed_ap_locations", "anchor_managed_ap_locations", "fabric_roles":
			values, ok := value.([]any)
			if !ok || len(values) > 10000 {
				return out, errWirelessDeviceModel
			}
			names := make([]string, 0, len(values))
			seen := map[string]bool{}
			for _, value := range values {
				name, ok := value.(string)
				if !ok || seen[name] {
					return out, errWirelessDeviceModel
				}
				if key == "fabric_roles" {
					// Other fabric roles are validated by the enclosing fabric model.
					if name == "" || len(name) > 128 || strings.TrimSpace(name) != name || strings.ContainsAny(name, "\x00\r\n") {
						return out, errWirelessDeviceModel
					}
				} else if !validSitePath(name) || name == "Global" {
					return out, errWirelessDeviceModel
				}
				seen[name] = true
				names = append(names, name)
			}
			sort.Strings(names)
			switch key {
			case "fabric_roles":
				out.roles = names
			case "primary_managed_ap_locations":
				out.locations.primary = names
			case "secondary_managed_ap_locations":
				out.locations.secondary = names
			case "anchor_managed_ap_locations":
				out.locations.anchor = names
			}
		default:
			return out, errWirelessDeviceModel
		}
	}
	if out.state != "PROVISION" && out.state != "REPROVISION" {
		return out, errWirelessDeviceModel
	}
	hasEmbedded, hasWLC := false, false
	for _, role := range out.roles {
		if role == "EMBEDDED_WIRELESS_CONTROLLER_NODE" {
			hasEmbedded = true
		}
		if role == "WIRELESS_CONTROLLER_NODE" {
			hasWLC = true
		}
	}
	hasAP := out.locations.primary != nil || out.locations.secondary != nil
	hasAnchor := out.locations.anchor != nil
	fabric := len(out.roles) > 0
	if fabric && hasAP && hasEmbedded == hasWLC {
		return out, errWirelessDeviceModel
	}
	if (hasEmbedded || hasWLC || out.deviceType == "WirelessController") && !hasAP && !hasAnchor {
		return out, errWirelessDeviceModel
	}
	if hasAnchor && (fabric || out.fabricSite != "") {
		return out, errWirelessDeviceModel
	}
	if ((hasAP || hasAnchor) && !fabric || hasWLC) && !hasEmbedded && out.deviceType != "WirelessController" {
		return out, errWirelessDeviceModel
	}
	if !hasEmbedded && !hasWLC && out.deviceType != "WirelessController" {
		return out, errWirelessDeviceModel
	}
	return out, nil
}
