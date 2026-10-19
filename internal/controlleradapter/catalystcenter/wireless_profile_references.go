// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
)

// Collect only the inventory classes used by new or retained fields. All
// readers share the same authenticated client and existing bounded paging.
func (c *client) wirelessProfileReferences(ctx context.Context, want wirelessProfileIntent, actual *wirelessProfileMetadata) (wirelessProfileReferences, error) {
	refs := wirelessProfileReferences{}
	needs := map[string]bool{}
	scanSSID := func(fields map[string]any) {
		for field, kind := range map[string]string{"interfaceName": "interfaces", "anchorGroupName": "anchors", "dot11beProfileId": "dot11be"} {
			if value, _ := fields[field].(string); value != "" {
				needs[kind] = true
			}
		}
	}
	for _, ssid := range want.ssids {
		var old map[string]any
		if actual != nil {
			old = wirelessProfilePriorEntry(actual.fields, "ssidDetails", "ssidName", ssid.name)
		}
		scanSSID(mergeWirelessProfileSSID(old, ssid))
		if ssid.dot11be != "" {
			needs["dot11be"] = true
		}
	}
	for _, zone := range want.zones {
		if name, _ := zone.fields["rfProfileName"].(string); name != "" {
			needs["rf"] = true
		}
	}
	if values, _ := want.fields["additionalInterfaces"].([]string); len(values) > 0 {
		needs["interfaces"] = true
	}
	if len(want.features) > 0 {
		needs["features"] = true
	}
	if actual != nil {
		if actual.unmapped || actual.name != want.name {
			return refs, errConfigurationOwned
		}
		// Omitted collections retain all prior references. Explicit collections
		// retain optional fields only on matching named entries.
		entries, _ := actual.fields["ssidDetails"].([]map[string]any)
		for _, entry := range entries {
			if want.ssids == nil {
				scanSSID(entry)
				continue
			}

		}
		if _, owned := want.fields["additionalInterfaces"]; !owned {
			if values, _ := actual.fields["additionalInterfaces"].([]string); len(values) > 0 {
				needs["interfaces"] = true
			}
		}
		entries, _ = actual.fields["apZones"].([]map[string]any)
		for _, entry := range entries {
			if want.zones == nil {
				if entry["rfProfileName"] != "" && entry["rfProfileName"] != nil {
					needs["rf"] = true
				}
				continue
			}
			for _, zone := range want.zones {
				if entry["apZoneName"] == zone.name {
					merged := mergeWirelessProfileFields(entry, zone.fields)
					if merged["rfProfileName"] != "" && merged["rfProfileName"] != nil {
						needs["rf"] = true
					}
				}
			}
		}
		if want.features == nil {
			if rows, _ := actual.fields["featureTemplates"].([]map[string]any); len(rows) > 0 {
				needs["features"] = true
			}
		}
	}
	var err error
	refs.global, err = (&wirelessSSIDExecutor{client: c}).global(ctx)
	if err != nil {
		return refs, err
	}
	refs.ssids, err = c.wirelessSSIDInventory(ctx, refs.global)
	if err != nil {
		return refs, err
	}
	if needs["interfaces"] {
		rows, err := c.wirelessInterfaces(ctx)
		if err != nil {
			return refs, err
		}
		refs.interfaces = map[string]string{}
		for name, row := range rows {
			refs.interfaces[name] = row.ID
		}
	}
	if needs["dot11be"] {
		rows, err := c.wirelessDot11beProfiles(ctx)
		if err != nil {
			return refs, err
		}
		refs.dot11be = map[string]string{}
		for name, row := range rows {
			refs.dot11be[name] = row.ID
		}
	}
	if needs["anchors"] {
		rows, err := c.wirelessAnchorGroups(ctx)
		if err != nil {
			return refs, err
		}
		refs.anchors = map[string]string{}
		for name, row := range rows {
			refs.anchors[name] = row.ID
		}
	}
	if needs["rf"] {
		rows, err := c.wirelessRFProfiles(ctx)
		if err != nil {
			return refs, err
		}
		refs.rf = map[string]string{}
		for name, row := range rows {
			refs.rf[name] = row.id
		}
	}
	if needs["features"] {
		refs.features = map[string]string{}
		ids := map[string]bool{}
		for _, kind := range []string{wirelessCleanAirKind, wirelessFRAKind} {
			rows, err := c.wirelessFeatureInstances(ctx, kind)
			if err != nil {
				return refs, err
			}
			for name, row := range rows {
				if _, exists := refs.features[name]; exists || ids[row.ID] {
					return refs, errInvalidResponse
				}
				refs.features[name] = row.ID
				ids[row.ID] = true
			}
		}
	}
	return refs, nil
}
