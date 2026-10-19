// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "sort"

type fabricL3AssociationIntent struct{ path, parent, name string }

// Site entries may be names or name objects; zone entries are name strings in
// the pinned module. No other object attributes can be silently discarded.
func parseFabricL3References(value any, objects bool) ([]string, error) {
	list, ok := value.([]any)
	if !ok || len(list) > 1000 {
		return nil, errInvalidResponse
	}
	names := []string{}
	seen := map[string]bool{}
	for _, v := range list {
		if object, ok := v.(map[string]any); ok && objects {
			if len(object) != 1 {
				return nil, errInvalidResponse
			}
			v = object["name"]
		}
		name, ok := v.(string)
		if !ok || !validProfileName(name) || seen[name] {
			return nil, errInvalidResponse
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
func parseFabricSitesWithL3Associations(value any) ([]fabricSiteIntent, []fabricZoneIntent, []fabricL2Intent, []fabricL3AssociationIntent, error) {
	list, ok := value.([]any)
	if !ok || len(list) > 1000 {
		return nil, nil, nil, nil, errInvalidResponse
	}
	base := []any{}
	out := []fabricL3AssociationIntent{}
	for _, v := range list {
		site, ok := v.(map[string]any)
		if !ok {
			return nil, nil, nil, nil, errInvalidResponse
		}
		path, ok := site["name"].(string)
		if !ok {
			return nil, nil, nil, nil, errInvalidResponse
		}
		copy := map[string]any{}
		for k, v := range site {
			if k != "l3_virtual_networks" && k != "fabric_zones" {
				copy[k] = v
			}
		}
		if v, exists := site["l3_virtual_networks"]; exists {
			names, err := parseFabricL3References(v, true)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			for _, name := range names {
				out = append(out, fabricL3AssociationIntent{path: path, name: name})
			}
		}
		if v, exists := site["fabric_zones"]; exists {
			zones, ok := v.([]any)
			if !ok || len(zones) > 1000 {
				return nil, nil, nil, nil, errInvalidResponse
			}
			zoneCopies := []any{}
			for _, v := range zones {
				zone, ok := v.(map[string]any)
				if !ok {
					return nil, nil, nil, nil, errInvalidResponse
				}
				zonePath, ok := zone["name"].(string)
				if !ok {
					return nil, nil, nil, nil, errInvalidResponse
				}
				zoneCopy := map[string]any{}
				for k, v := range zone {
					if k != "l3_virtual_networks" {
						zoneCopy[k] = v
					}
				}
				if v, exists := zone["l3_virtual_networks"]; exists {
					names, err := parseFabricL3References(v, false)
					if err != nil {
						return nil, nil, nil, nil, err
					}
					for _, name := range names {
						out = append(out, fabricL3AssociationIntent{path: zonePath, parent: path, name: name})
					}
				}
				zoneCopies = append(zoneCopies, zoneCopy)
				if len(out) > 1000 {
					return nil, nil, nil, nil, errInvalidResponse
				}
			}
			copy["fabric_zones"] = zoneCopies
		}
		base = append(base, copy)
		if len(out) > 1000 {
			return nil, nil, nil, nil, errInvalidResponse
		}
	}
	sites, zones, l2, err := parseFabricSitesWithL2(base)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.parent == "") != (b.parent == "") {
			return a.parent == ""
		}
		if a.path != b.path {
			return a.path < b.path
		}
		return a.name < b.name
	})
	return sites, zones, l2, out, nil
}
