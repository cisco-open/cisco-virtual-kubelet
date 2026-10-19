// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

// Expose private Go intent fields only to an in-memory submission snapshot.
// In particular nil collections must not be mistaken for explicit empty ones.
func wirelessProfileDispatchIntent(desired []wirelessProfileIntent) any {
	var rows []map[string]any
	for _, p := range desired {
		var ssids, zones []map[string]any
		for _, s := range p.ssids {
			ssids = append(ssids, map[string]any{"name": s.name, "dot11be": s.dot11be, "fields": s.fields})
		}
		for _, z := range p.zones {
			zones = append(zones, map[string]any{"name": z.name, "fields": z.fields, "ssids": z.ssids})
		}
		tags := func(input []wirelessProfileTag) any {
			var out []map[string]any
			if input != nil {
				out = []map[string]any{}
			}
			for _, tag := range input {
				out = append(out, map[string]any{"name": tag.name, "fields": tag.fields, "sites": tag.sites, "zones": tag.zones})
			}
			return out
		}
		if p.ssids != nil && ssids == nil {
			ssids = []map[string]any{}
		}
		if p.zones != nil && zones == nil {
			zones = []map[string]any{}
		}
		rows = append(rows, map[string]any{"name": p.name, "fields": p.fields, "sites": p.sites, "features": p.features, "ssids": ssids, "zones": zones, "siteTags": tags(p.siteTags), "policyTags": tags(p.policyTags)})
	}
	return rows
}
