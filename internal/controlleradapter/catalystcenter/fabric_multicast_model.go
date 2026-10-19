// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"net/netip"
	"sort"
)

type fabricMulticastIntent struct {
	replication string
	wireless    *bool
	networks    []fabricMulticastVNIntent
}
type fabricMulticastVNIntent struct {
	name, pool  string
	payload     map[string]any
	rps         []fabricMulticastRPIntent
	rpsDeclared bool
}
type fabricMulticastRPIntent struct {
	name    string   // Model-local label; the native API has no RP name field.
	devices []string // Resolve inventory names before constructing networkDeviceIds.
	payload map[string]any
}

func parseFabricMulticast(value any) (fabricMulticastIntent, error) {
	zero := fabricMulticastIntent{}
	m, ok := value.(map[string]any)
	if !ok {
		return zero, errInvalidResponse
	}
	out := fabricMulticastIntent{}
	for k, v := range m {
		switch k {
		case "replication_mode":
			if v == nil {
				continue
			}
			mode, ok := v.(string)
			if !ok || !validFabricReplicationMode(mode) {
				return zero, errInvalidResponse
			}
			out.replication = mode
		case "wireless_multicast_enabled":
			if v == nil {
				continue
			}
			b, ok := v.(bool)
			if !ok {
				return zero, errInvalidResponse
			}
			out.wireless = &b
		case "virtual_networks":
			if v == nil {
				continue
			}
			rows, ok := v.([]any)
			if !ok || len(rows) > 1000 {
				return zero, errInvalidResponse
			}
			seen := map[string]bool{}
			for _, row := range rows {
				vn, err := parseFabricMulticastVN(row)
				if err != nil {
					return zero, err
				}
				if seen[vn.name] {
					return zero, errInvalidResponse
				}
				seen[vn.name] = true
				out.networks = append(out.networks, vn)
			}
		default:
			return zero, errInvalidResponse
		}
	}
	sort.Slice(out.networks, func(i, j int) bool { return out.networks[i].name < out.networks[j].name })
	return out, nil
}

func multicastRanges(value any, v4, ssm bool) ([]string, error) {
	if ssm && !v4 {
		return nil, errInvalidResponse
	}
	rows, ok := value.([]any)
	if !ok || len(rows) > 1000 {
		return nil, errInvalidResponse
	}
	seen := map[string]bool{}
	out := []string{}
	for _, row := range rows {
		s, ok := row.(string)
		if !ok {
			return nil, errInvalidResponse
		}
		p, err := netip.ParsePrefix(s)
		if err != nil || p.Addr().Is4() != v4 || p != p.Masked() || !p.Addr().IsMulticast() {
			return nil, errInvalidResponse
		}
		if v4 && p.Bits() < 4 || !v4 && p.Bits() < 8 {
			return nil, errInvalidResponse
		}
		// The native SSM contract permits subnets within 225/8 through 239/8.
		if ssm && (p.Bits() < 8 || p.Addr().As4()[0] < 225 || p.Addr().As4()[0] > 239) {
			return nil, errInvalidResponse
		}
		normalized := p.String()
		if !seen[normalized] {
			out = append(out, normalized)
			seen[normalized] = true
		}
	}
	sort.Strings(out)
	return out, nil
}

func parseFabricMulticastVN(value any) (fabricMulticastVNIntent, error) {
	zero := fabricMulticastVNIntent{}
	m, ok := value.(map[string]any)
	if !ok {
		return zero, errInvalidResponse
	}
	out := fabricMulticastVNIntent{payload: map[string]any{}}
	for k, v := range m {
		switch k {
		case "name", "ip_pool_name":
			name, ok := v.(string)
			if !ok || !validProfileName(name) {
				return zero, errInvalidResponse
			}
			if k == "name" {
				out.name = name
				out.payload["virtualNetworkName"] = name
			} else {
				out.pool = name
				out.payload["ipPoolName"] = name
			}
		case "ipv4_ssm_ranges":
			if v == nil {
				continue
			}
			ranges, err := multicastRanges(v, true, true)
			if err != nil {
				return zero, err
			}
			out.payload["ipv4SsmRanges"] = ranges
		case "multicast_rps":
			if v == nil {
				continue
			}
			rows, ok := v.([]any)
			if !ok || len(rows) > 1000 {
				return zero, errInvalidResponse
			}
			out.rpsDeclared = true
			labels := map[string]bool{}
			for _, row := range rows {
				rp, err := parseFabricMulticastRP(row)
				if err != nil {
					return zero, err
				}
				if rp.name != "" {
					if labels[rp.name] {
						return zero, errInvalidResponse
					}
					labels[rp.name] = true
				}
				out.rps = append(out.rps, rp)
			}
		default:
			return zero, errInvalidResponse
		}
	}
	if out.name == "" || out.pool == "" {
		return zero, errInvalidResponse
	}
	ssm, _ := out.payload["ipv4SsmRanges"].([]string)
	defaults := map[string]bool{}
	for _, rp := range out.rps {
		for _, flag := range []string{"isDefaultV4RP", "isDefaultV6RP"} {
			if rp.payload[flag] == true {
				if defaults[flag] {
					return zero, errInvalidResponse
				}
				defaults[flag] = true
			}
		}
		asm, _ := rp.payload["ipv4AsmRanges"].([]string)
		for _, a := range asm {
			ap, _ := netip.ParsePrefix(a)
			for _, s := range ssm {
				sp, _ := netip.ParsePrefix(s)
				if ap.Overlaps(sp) {
					return zero, errInvalidResponse
				}
			}
		}
	}
	return out, nil
}

func parseFabricMulticastRP(value any) (fabricMulticastRPIntent, error) {
	zero := fabricMulticastRPIntent{}
	m, ok := value.(map[string]any)
	if !ok {
		return zero, errInvalidResponse
	}
	location, ok := m["rp_location"].(string)
	if !ok || location != "EXTERNAL" && location != "FABRIC" {
		return zero, errInvalidResponse
	}
	out := fabricMulticastRPIntent{payload: map[string]any{"rpDeviceLocation": location}}
	for k, v := range m {
		switch k {
		case "rp_location":
		case "name":
			if v == nil {
				continue
			}
			s, ok := v.(string)
			if !ok || !validProfileName(s) {
				return zero, errInvalidResponse
			}
			out.name = s
		case "ipv4_address", "ipv6_address":
			if v == nil || location == "FABRIC" {
				continue
			} // Pinned module suppresses these for fabric RPs.
			s, ok := v.(string)
			if !ok {
				return zero, errInvalidResponse
			}
			a, err := netip.ParseAddr(s)
			if err != nil || a.Zone() != "" || a.Is4In6() || a.IsMulticast() || a.IsUnspecified() || a.Is4() != (k == "ipv4_address") {
				return zero, errInvalidResponse
			}
			target := "ipv4Address"
			if k == "ipv6_address" {
				target = "ipv6Address"
			}
			out.payload[target] = a.String()
		case "is_default_v4_rp", "is_default_v6_rp":
			if v == nil {
				continue
			}
			b, ok := v.(bool)
			if !ok {
				return zero, errInvalidResponse
			}
			target := "isDefaultV4RP"
			if k == "is_default_v6_rp" {
				target = "isDefaultV6RP"
			}
			out.payload[target] = b
		case "ipv4_asm_ranges", "ipv6_asm_ranges":
			if v == nil {
				continue
			}
			ranges, err := multicastRanges(v, k == "ipv4_asm_ranges", false)
			if err != nil {
				return zero, err
			}
			target := "ipv4AsmRanges"
			if k == "ipv6_asm_ranges" {
				target = "ipv6AsmRanges"
			}
			out.payload[target] = ranges
		case "fabric_rps":
			if v == nil || location != "FABRIC" {
				continue
			}
			rows, ok := v.([]any)
			if !ok || len(rows) > 1000 {
				return zero, errInvalidResponse
			}
			seen := map[string]bool{}
			for _, row := range rows {
				name, ok := row.(string)
				if !ok || !validProfileName(name) {
					return zero, errInvalidResponse
				}
				if !seen[name] {
					out.devices = append(out.devices, name)
					seen[name] = true
				}
			}
			sort.Strings(out.devices)
		default:
			return zero, errInvalidResponse
		}
	}
	if location == "FABRIC" {
		if len(out.devices) == 0 || len(out.devices) > 2 {
			return zero, errInvalidResponse
		}
	} else {
		_, v4 := out.payload["ipv4Address"]
		_, v6 := out.payload["ipv6Address"]
		if v4 == v6 {
			return zero, errInvalidResponse
		}
		wrongRanges := "ipv6AsmRanges"
		wrongDefault := "isDefaultV6RP"
		if v6 {
			wrongRanges = "ipv4AsmRanges"
			wrongDefault = "isDefaultV4RP"
		}
		ranges, _ := out.payload[wrongRanges].([]string)
		if len(ranges) > 0 || out.payload[wrongDefault] == true {
			return zero, errInvalidResponse
		}
	}
	return out, nil
}
