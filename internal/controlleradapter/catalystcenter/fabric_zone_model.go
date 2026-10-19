// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"sort"
	"strings"
)

type fabricZoneIntent struct{ path, parent, authentication string }

// Split the nested NetAsCode resource while retaining strict validation of the
// enclosing site. No unsupported gateway/VN/event intent may be discarded.
func parseFabricSitesWithZones(value any) ([]fabricSiteIntent, []fabricZoneIntent, error) {
	list, ok := value.([]any)
	if !ok {
		return nil, nil, errInvalidResponse
	}
	base := []any{}
	zones := []fabricZoneIntent{}
	seen := map[string]bool{}
	for _, entry := range list {
		site, ok := entry.(map[string]any)
		if !ok {
			return nil, nil, errInvalidResponse
		}
		parent, ok := site["name"].(string)
		if !ok || !validSitePath(parent) || parent == "Global" {
			return nil, nil, errInvalidResponse
		}
		copy := map[string]any{}
		for key, v := range site {
			if key != "fabric_zones" {
				copy[key] = v
			}
		}
		base = append(base, copy)
		if value, present := site["fabric_zones"]; present {
			entries, ok := value.([]any)
			if !ok || len(entries) > 1000 {
				return nil, nil, errInvalidResponse
			}
			for _, entry := range entries {
				zone, ok := entry.(map[string]any)
				if !ok || len(zone) != 2 {
					return nil, nil, errInvalidResponse
				}
				path, ok := zone["name"].(string)
				if !ok || !validSitePath(path) || !strings.HasPrefix(path, parent+"/") || seen[path] {
					return nil, nil, errInvalidResponse
				}
				auth, ok := zone["authentication_template"].(map[string]any)
				if !ok || len(auth) != 1 {
					return nil, nil, errInvalidResponse
				}
				name, ok := auth["name"].(string)
				if !ok || !validFabricAuthentication(name) {
					return nil, nil, errInvalidResponse
				}
				seen[path] = true
				zones = append(zones, fabricZoneIntent{path: path, parent: parent, authentication: name})
				if len(zones) > 1000 {
					return nil, nil, errInvalidResponse
				}
			}
		}
	}
	sites, err := parseFabricSiteModels(base)
	if err != nil {
		return nil, nil, err
	}
	for _, zone := range zones {
		for _, site := range sites {
			if site.path == zone.path || site.path != zone.parent && strings.HasPrefix(site.path, zone.parent+"/") && strings.HasPrefix(zone.path, site.path+"/") {
				return nil, nil, errInvalidResponse
			}
		}
		for _, other := range zones {
			if other.path != zone.path && strings.HasPrefix(zone.path, other.path+"/") {
				return nil, nil, errInvalidResponse
			}
		}
	}
	sort.Slice(zones, func(i, j int) bool { return zones[i].path < zones[j].path })
	return sites, zones, nil
}
