// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
)

const fabricMulticastVNsPath = "/dna/intent/api/v1/sda/multicast/virtualNetworks"

type fabricMulticastVNKey struct{ fabricID, network string }
type fabricMulticastVN struct {
	ID, FabricID, Network, Pool string
	fields                      map[string]any
	unknownFields               bool
}

// Preserve reported fabric-RP addresses. They are allocated by the appliance,
// unlike create intent, and must survive a later ownership-aware update.
func parseFabricMulticastRPObservation(value any) (map[string]any, bool, error) {
	m, ok := value.(map[string]any)
	if !ok {
		return nil, false, errInvalidResponse
	}
	out := map[string]any{}
	unknown := false
	for k, v := range m {
		switch k {
		case "rpDeviceLocation":
			if v != "FABRIC" && v != "EXTERNAL" {
				return nil, false, errInvalidResponse
			}
			out[k] = v
		case "ipv4Address", "ipv6Address":
			if v == nil {
				continue
			}
			s, ok := v.(string)
			if !ok {
				return nil, false, errInvalidResponse
			}
			a, err := netip.ParseAddr(s)
			if err != nil || a.Zone() != "" || a.Is4In6() || a.IsUnspecified() || a.IsMulticast() || a.Is4() != (k == "ipv4Address") {
				return nil, false, errInvalidResponse
			}
			out[k] = a.String()
		case "isDefaultV4RP", "isDefaultV6RP":
			if v == nil {
				continue
			}
			b, ok := v.(bool)
			if !ok {
				return nil, false, errInvalidResponse
			}
			out[k] = b
		case "ipv4AsmRanges", "ipv6AsmRanges":
			if v == nil {
				continue
			}
			ranges, err := multicastRanges(v, k == "ipv4AsmRanges", false)
			if err != nil {
				return nil, false, err
			}
			out[k] = ranges
		case "networkDeviceIds":
			if v == nil {
				continue
			}
			rows, ok := v.([]any)
			if !ok || len(rows) > 2 {
				return nil, false, errInvalidResponse
			}
			ids := []string{}
			seen := map[string]bool{}
			for _, row := range rows {
				id, ok := row.(string)
				if !ok || !validAPIID(id) || seen[id] {
					return nil, false, errInvalidResponse
				}
				seen[id] = true
				ids = append(ids, id)
			}
			sort.Strings(ids)
			out[k] = ids
		default:
			unknown = true
		}
	}
	location, _ := out["rpDeviceLocation"].(string)
	ids, _ := out["networkDeviceIds"].([]string)
	if location == "FABRIC" {
		if len(ids) == 0 {
			return nil, false, errInvalidResponse
		}
	} else if location == "EXTERNAL" {
		_, v4 := out["ipv4Address"]
		_, v6 := out["ipv6Address"]
		if v4 == v6 || len(ids) > 0 {
			return nil, false, errInvalidResponse
		}
	} else {
		return nil, false, errInvalidResponse
	}
	return out, unknown, nil
}

func parseFabricMulticastVNObservation(raw json.RawMessage) (fabricMulticastVN, error) {
	zero := fabricMulticastVN{}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return zero, errInvalidResponse
	}
	out := fabricMulticastVN{fields: map[string]any{}}
	for k, v := range m {
		switch k {
		case "id", "fabricId", "virtualNetworkName", "ipPoolName":
			s, ok := v.(string)
			if !ok || !validProfileName(s) {
				return zero, errInvalidResponse
			}
			out.fields[k] = s
		case "ipv4SsmRanges":
			if v == nil {
				continue
			}
			ranges, err := multicastRanges(v, true, true)
			if err != nil {
				return zero, err
			}
			out.fields[k] = ranges
		case "multicastRPs":
			if v == nil {
				continue
			}
			rows, ok := v.([]any)
			if !ok || len(rows) > 1000 {
				return zero, errInvalidResponse
			}
			rps := []map[string]any{}
			seen := map[string]bool{}
			for _, row := range rows {
				rp, unknown, err := parseFabricMulticastRPObservation(row)
				if err != nil {
					return zero, err
				}
				identity := fabricMulticastRPKey(rp)
				if seen[identity] {
					return zero, errInvalidResponse
				}
				seen[identity] = true
				out.unknownFields = out.unknownFields || unknown
				rps = append(rps, rp)
			}
			sort.Slice(rps, func(i, j int) bool {
				a, _ := json.Marshal(rps[i])
				b, _ := json.Marshal(rps[j])
				return string(a) < string(b)
			})
			out.fields[k] = rps
		default:
			out.unknownFields = true
		}
	}
	out.ID, _ = out.fields["id"].(string)
	out.FabricID, _ = out.fields["fabricId"].(string)
	out.Network, _ = out.fields["virtualNetworkName"].(string)
	out.Pool, _ = out.fields["ipPoolName"].(string)
	if !validAPIID(out.ID) || !validAPIID(out.FabricID) || out.Network == "" || out.Pool == "" {
		return zero, errInvalidResponse
	}
	return out, nil
}

func (c *client) fabricMulticastVNs(ctx context.Context) (map[fabricMulticastVNKey]fabricMulticastVN, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		return c.get(ctx, path, q, http.StatusOK)
	}
	rows, err := listInventoryQueryWithGet(ctx, get, fabricMulticastVNsPath, nil, func(raw json.RawMessage) string {
		var id struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &id) != nil {
			return ""
		}
		return id.ID
	})
	if err != nil {
		return nil, err
	}
	out := map[fabricMulticastVNKey]fabricMulticastVN{}
	for _, raw := range rows {
		row, err := parseFabricMulticastVNObservation(raw)
		if err != nil {
			return nil, err
		}
		key := fabricMulticastVNKey{row.FabricID, row.Network}
		if _, exists := out[key]; exists {
			return nil, errInvalidResponse
		}
		out[key] = row
	}
	return out, nil
}
