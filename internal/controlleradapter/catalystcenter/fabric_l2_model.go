// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"sort"
)

// A zone references a named definition on its parent site. It cannot override
// the parent's VLAN attributes or choose an independent auto-assigned VLAN ID.
type fabricL2Intent struct {
	path, parent, name    string
	vlanName, traffic, l3 string
	vlan                  int
	wireless              *bool
}

func validFabricVLAN(id int) bool {
	return id >= 2 && id <= 4093 && !(id >= 1002 && id <= 1005) && id != 2046
}
func validFabricVLANName(name string) bool {
	if name == "" || len(name) > 256 {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func parseFabricL2Definition(value any, path string) (fabricL2Intent, error) {
	out := fabricL2Intent{path: path}
	m, ok := value.(map[string]any)
	if !ok {
		return out, errInvalidResponse
	}
	for key := range m {
		switch key {
		case "name", "vlan_name", "vlan_id", "traffic_type", "fabric_enabled_wireless", "associated_l3_virtual_network_name":
		default:
			return fabricL2Intent{}, errInvalidResponse
		}
	}
	out.name, ok = m["name"].(string)
	if !ok || !validProfileName(out.name) {
		return fabricL2Intent{}, errInvalidResponse
	}
	out.vlanName, ok = m["vlan_name"].(string)
	if !ok || !validFabricVLANName(out.vlanName) {
		return fabricL2Intent{}, errInvalidResponse
	}
	out.traffic, ok = m["traffic_type"].(string)
	if !ok || (out.traffic != "DATA" && out.traffic != "VOICE") {
		return fabricL2Intent{}, errInvalidResponse
	}
	if v := m["vlan_id"]; v != nil {
		n, err := plannedAPNumber(v, true)
		if err != nil || n < 2 || n > 4093 || !validFabricVLAN(int(n)) {
			return fabricL2Intent{}, errInvalidResponse
		}
		out.vlan = int(n)
	}
	if v := m["fabric_enabled_wireless"]; v != nil {
		b, ok := v.(bool)
		if !ok {
			return fabricL2Intent{}, errInvalidResponse
		}
		out.wireless = &b
	}
	if v := m["associated_l3_virtual_network_name"]; v != nil {
		s, ok := v.(string)
		if !ok || !validProfileName(s) {
			return fabricL2Intent{}, errInvalidResponse
		}
		out.l3 = s
	}
	return out, nil
}

// Extract only implemented L2 fields from private copies; delegate every other
// field to the existing strict site/zone parser, so unsupported intent fails.
func parseFabricSitesWithL2(value any) ([]fabricSiteIntent, []fabricZoneIntent, []fabricL2Intent, error) {
	list, ok := value.([]any)
	if !ok || len(list) > 1000 {
		return nil, nil, nil, errInvalidResponse
	}
	base := []any{}
	networks := []fabricL2Intent{}
	for _, entry := range list {
		site, ok := entry.(map[string]any)
		if !ok {
			return nil, nil, nil, errInvalidResponse
		}
		path, ok := site["name"].(string)
		if !ok {
			return nil, nil, nil, errInvalidResponse
		}
		copy := map[string]any{}
		for k, v := range site {
			if k != "l2_virtual_networks" && k != "fabric_zones" {
				copy[k] = v
			}
		}
		definitions := map[string]fabricL2Intent{}
		vlans, ids := map[string]bool{}, map[int]bool{}
		if value, present := site["l2_virtual_networks"]; present {
			entries, ok := value.([]any)
			if !ok || len(entries) > 1000 {
				return nil, nil, nil, errInvalidResponse
			}
			for _, entry := range entries {
				vn, err := parseFabricL2Definition(entry, path)
				if err != nil {
					return nil, nil, nil, err
				}
				if _, exists := definitions[vn.name]; exists || vlans[vn.vlanName] || vn.vlan != 0 && ids[vn.vlan] {
					return nil, nil, nil, errInvalidResponse
				}
				definitions[vn.name] = vn
				vlans[vn.vlanName] = true
				ids[vn.vlan] = true
				networks = append(networks, vn)
			}
		}
		if value, present := site["fabric_zones"]; present {
			entries, ok := value.([]any)
			if !ok || len(entries) > 1000 {
				return nil, nil, nil, errInvalidResponse
			}
			zones := []any{}
			for _, entry := range entries {
				zone, ok := entry.(map[string]any)
				if !ok {
					return nil, nil, nil, errInvalidResponse
				}
				zonePath, ok := zone["name"].(string)
				if !ok {
					return nil, nil, nil, errInvalidResponse
				}
				zoneCopy := map[string]any{}
				for k, v := range zone {
					if k != "l2_virtual_networks" {
						zoneCopy[k] = v
					}
				}
				if value, present := zone["l2_virtual_networks"]; present {
					refs, ok := value.([]any)
					if !ok || len(refs) > 1000 {
						return nil, nil, nil, errInvalidResponse
					}
					seen := map[string]bool{}
					for _, ref := range refs {
						name, ok := ref.(string)
						if !ok || seen[name] {
							return nil, nil, nil, errInvalidResponse
						}
						vn, exists := definitions[name]
						if !exists {
							return nil, nil, nil, errInvalidResponse
						}
						seen[name] = true
						vn.parent = path
						vn.path = zonePath
						networks = append(networks, vn)
					}
				}
				zones = append(zones, zoneCopy)
			}
			copy["fabric_zones"] = zones
		}
		base = append(base, copy)
		if len(networks) > 1000 {
			return nil, nil, nil, errInvalidResponse
		}
	}
	sites, zones, err := parseFabricSitesWithZones(base)
	if err != nil {
		return nil, nil, nil, err
	}
	sort.Slice(networks, func(i, j int) bool {
		a, b := networks[i], networks[j]
		if (a.parent == "") != (b.parent == "") {
			return a.parent == ""
		}
		if a.path != b.path {
			return a.path < b.path
		}
		return a.name < b.name
	})
	return sites, zones, networks, nil
}
