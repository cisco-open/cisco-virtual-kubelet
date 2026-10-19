// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "sort"

type fabricAnycastIntent struct {
	path, parent string
	anycastGatewayIntent
}
type fabricGatewayModel struct {
	sites        []fabricSiteIntent
	zones        []fabricZoneIntent
	l2           []fabricL2Intent
	associations []fabricL3AssociationIntent
	gateways     []fabricAnycastIntent
}

// Zone gateway entries are primary-pool name references, not independent
// definitions. Reparse their parent definitions to avoid mutable payload aliasing.
func parseFabricSitesWithGateways(value any) (*fabricGatewayModel, error) {
	list, ok := value.([]any)
	if !ok || len(list) > 1000 {
		return nil, errInvalidResponse
	}
	base := []any{}
	out := &fabricGatewayModel{}
	for _, v := range list {
		site, ok := v.(map[string]any)
		if !ok {
			return nil, errInvalidResponse
		}
		path, ok := site["name"].(string)
		if !ok {
			return nil, errInvalidResponse
		}
		copy := map[string]any{}
		for k, v := range site {
			if k != "anycast_gateways" && k != "fabric_zones" {
				copy[k] = v
			}
		}
		definitions := map[string]any{}
		if v, present := site["anycast_gateways"]; present {
			gateways, ok := v.([]any)
			if !ok || len(gateways) > 1000 {
				return nil, errInvalidResponse
			}
			for _, v := range gateways {
				gw, err := parseAnycastGateway(v)
				if err != nil {
					return nil, err
				}
				if _, exists := definitions[gw.pool]; exists {
					return nil, errInvalidResponse
				}
				definitions[gw.pool] = v
				out.gateways = append(out.gateways, fabricAnycastIntent{path: path, anycastGatewayIntent: gw})
			}
		}
		if v, present := site["fabric_zones"]; present {
			zones, ok := v.([]any)
			if !ok || len(zones) > 1000 {
				return nil, errInvalidResponse
			}
			copies := []any{}
			for _, v := range zones {
				zone, ok := v.(map[string]any)
				if !ok {
					return nil, errInvalidResponse
				}
				zonePath, ok := zone["name"].(string)
				if !ok {
					return nil, errInvalidResponse
				}
				z := map[string]any{}
				for k, v := range zone {
					if k != "anycast_gateways" {
						z[k] = v
					}
				}
				if v, present := zone["anycast_gateways"]; present {
					refs, ok := v.([]any)
					if !ok || len(refs) > 1000 {
						return nil, errInvalidResponse
					}
					seen := map[string]bool{}
					for _, v := range refs {
						name, ok := v.(string)
						if !ok || seen[name] {
							return nil, errInvalidResponse
						}
						definition, exists := definitions[name]
						if !exists {
							return nil, errInvalidResponse
						}
						gw, err := parseAnycastGateway(definition)
						if err != nil {
							return nil, err
						}
						seen[name] = true
						out.gateways = append(out.gateways, fabricAnycastIntent{path: zonePath, parent: path, anycastGatewayIntent: gw})
					}
				}
				copies = append(copies, z)
				if len(out.gateways) > 1000 {
					return nil, errInvalidResponse
				}
			}
			copy["fabric_zones"] = copies
		}
		base = append(base, copy)
		if len(out.gateways) > 1000 {
			return nil, errInvalidResponse
		}
	}
	var err error
	out.sites, out.zones, out.l2, out.associations, err = parseFabricSitesWithL3Associations(base)
	if err != nil {
		return nil, err
	}
	// Gateway and explicit L2 intents must not independently own the same VLAN
	// within one fabric. Inherited definitions on different zones are independent.
	type nameKey struct{ path, name string }
	type idKey struct {
		path string
		id   int
	}
	names := map[nameKey]bool{}
	ids := map[idKey]bool{}
	claim := func(path, name string, id int) bool {
		nk, ik := nameKey{path, name}, idKey{path, id}
		if name != "" && names[nk] || id != 0 && ids[ik] {
			return false
		}
		if name != "" {
			names[nk] = true
		}
		if id != 0 {
			ids[ik] = true
		}
		return true
	}
	for _, vn := range out.l2 {
		if !claim(vn.path, vn.vlanName, vn.vlan) {
			return nil, errInvalidResponse
		}
	}
	for _, gw := range out.gateways {
		name, _ := gw.payload["vlanName"].(string)
		id, _ := gw.payload["vlanId"].(int)
		if !claim(gw.path, name, id) {
			return nil, errInvalidResponse
		}
	}
	sort.Slice(out.gateways, func(i, j int) bool {
		a, b := out.gateways[i], out.gateways[j]
		if (a.parent == "") != (b.parent == "") {
			return a.parent == ""
		}
		if a.path != b.path {
			return a.path < b.path
		}
		return a.pool < b.pool
	})
	return out, nil
}
