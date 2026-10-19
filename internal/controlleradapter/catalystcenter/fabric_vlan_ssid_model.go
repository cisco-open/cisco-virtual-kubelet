// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"sort"
	"strings"
)

type fabricVLANSSIDIntent struct {
	path     string
	mappings []fabricVLANSSID
}

// NetAsCode 0.5.0 maps only name and vlan_name. Group tags are native policy,
// not an attribute supplied by this model; omission must not erase them.
func parseFabricWirelessSSIDs(value any) ([]fabricVLANSSID, error) {
	rows, ok := value.([]any)
	if !ok || len(rows) > 1000 {
		return nil, errInvalidResponse
	}
	byVLAN := map[string][]fabricSSIDBinding{}
	names := map[string]bool{}
	for _, entry := range rows {
		row, ok := entry.(map[string]any)
		if !ok || len(row) != 2 {
			return nil, errInvalidResponse
		}
		name, ok := row["name"].(string)
		if !ok || name == "" || len(name) > 32 || strings.ContainsAny(name, "\x00\r\n") || names[name] {
			return nil, errInvalidResponse
		}
		vlan, ok := row["vlan_name"].(string)
		if !ok || !validProfileName(vlan) {
			return nil, errInvalidResponse
		}
		names[name] = true
		byVLAN[vlan] = append(byVLAN[vlan], fabricSSIDBinding{Name: name})
	}
	out := make([]fabricVLANSSID, 0, len(byVLAN))
	for vlan, ssids := range byVLAN {
		sort.Slice(ssids, func(i, j int) bool { return ssids[i].Name < ssids[j].Name })
		out = append(out, fabricVLANSSID{VLAN: vlan, SSIDs: ssids})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VLAN < out[j].VLAN })
	return out, nil
}

// Strip only the supported extension; the complete remaining site composition
// still passes through its established strict parsers. No caller data is edited.
func parseFabricSitesWithWirelessSSIDs(value any) (fabricGatewayModel, []fabricMulticastSiteIntent, []fabricVLANSSIDIntent, error) {
	rows, ok := value.([]any)
	if !ok || len(rows) > 1000 {
		return fabricGatewayModel{}, nil, nil, errInvalidResponse
	}
	base := make([]any, 0, len(rows))
	intents := []fabricVLANSSIDIntent{}
	total := 0
	for _, entry := range rows {
		row, ok := entry.(map[string]any)
		if !ok {
			return fabricGatewayModel{}, nil, nil, errInvalidResponse
		}
		copy := map[string]any{}
		for key, value := range row {
			if key != "wireless_ssids" {
				copy[key] = value
			}
		}
		base = append(base, copy)
		if value, present := row["wireless_ssids"]; present {
			mappings, err := parseFabricWirelessSSIDs(value)
			if err != nil {
				return fabricGatewayModel{}, nil, nil, err
			}
			path, ok := row["name"].(string)
			if !ok {
				return fabricGatewayModel{}, nil, nil, errInvalidResponse
			}
			for _, mapping := range mappings {
				total += len(mapping.SSIDs)
			}
			if total > 1000 {
				return fabricGatewayModel{}, nil, nil, errInvalidResponse
			}
			// The pinned module emits no resource for an empty declaration. It does
			// not mean delete all native mappings.
			if len(mappings) > 0 {
				intents = append(intents, fabricVLANSSIDIntent{path: path, mappings: mappings})
			}
		}
	}
	model, multicast, err := parseFabricSitesWithMulticast(base)
	if err != nil {
		return fabricGatewayModel{}, nil, nil, err
	}
	sort.Slice(intents, func(i, j int) bool { return intents[i].path < intents[j].path })
	return model, multicast, intents, nil
}
