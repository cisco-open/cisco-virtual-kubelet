// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"net/netip"
	"reflect"
	"sort"
)

// Device names must already have been resolved against validated inventory.
// This mapper does not authorize physical changes or replace role/pool checks.
func fabricMulticastPayload(want fabricMulticastVNIntent, fabricID string, deviceIDs map[string]string, observed *fabricMulticastVN) (map[string]any, error) {
	if !validAPIID(fabricID) || want.name == "" || want.pool == "" {
		return nil, errInvalidResponse
	}
	body := map[string]any{"fabricId": fabricID, "virtualNetworkName": want.name, "ipPoolName": want.pool}
	var prior []map[string]any
	if observed != nil {
		if observed.unknownFields {
			return nil, errConfigurationTransition
		}
		if !validAPIID(observed.ID) || observed.FabricID != fabricID || observed.Network != want.name || observed.Pool != want.pool {
			return nil, errConfigurationStale
		}
		for k, v := range observed.fields {
			body[k] = v
		}
		var known bool
		prior, known = observed.fields["multicastRPs"].([]map[string]any)
		if !known {
			return nil, errInvalidResponse
		}
		for _, rp := range prior {
			if rp["rpDeviceLocation"] == "FABRIC" {
				if address, ok := rp["ipv4Address"].(string); !ok || address == "" {
					return nil, errInvalidResponse
				}
				v6Ranges, _ := rp["ipv6AsmRanges"].([]string)
				if len(v6Ranges) > 0 || rp["isDefaultV6RP"] == true {
					if address, ok := rp["ipv6Address"].(string); !ok || address == "" {
						return nil, errInvalidResponse
					}
				}
			}
		}
	}
	for k, v := range want.payload {
		if observed != nil && k == "ipv4SsmRanges" {
			old, known := observed.fields[k].([]string)
			next, ok := v.([]string)
			if !known || !ok {
				return nil, errInvalidResponse
			}
			if !multicastContainsAll(next, old) {
				return nil, errConfigurationTransition
			}
		}
		body[k] = v
	}
	if want.rpsDeclared {
		rps := []map[string]any{}
		used := map[int]bool{}
		seen := map[string]bool{}
		for _, rp := range want.rps {
			next := map[string]any{}
			for k, v := range rp.payload {
				next[k] = v
			}
			if next["rpDeviceLocation"] == "FABRIC" {
				ids := []string{}
				unique := map[string]bool{}
				for _, name := range rp.devices {
					id := deviceIDs[name]
					if !validAPIID(id) || unique[id] {
						return nil, errConfigurationStale
					}
					unique[id] = true
					ids = append(ids, id)
				}
				if len(ids) == 0 || len(ids) > 2 {
					return nil, errInvalidResponse
				}
				sort.Strings(ids)
				next["networkDeviceIds"] = ids
			}
			key := fabricMulticastRPKey(next)
			if seen[key] {
				return nil, errInvalidResponse
			}
			seen[key] = true
			match := -1
			for i, old := range prior {
				if fabricMulticastRPKey(old) == key {
					if match != -1 {
						return nil, errInvalidResponse
					}
					match = i
				}
			}
			if match >= 0 {
				used[match] = true
				merged := map[string]any{}
				for k, v := range prior[match] {
					merged[k] = v
				}
				for k, v := range next {
					if k == "ipv4AsmRanges" || k == "ipv6AsmRanges" {
						old, known := merged[k].([]string)
						ranges, ok := v.([]string)
						if !known || !ok {
							return nil, errInvalidResponse
						}
						if !multicastContainsAll(ranges, old) {
							return nil, errConfigurationTransition
						}
					}
					merged[k] = v
				}
				next = merged
			}
			rps = append(rps, next)
		}
		if len(used) != len(prior) {
			return nil, errConfigurationTransition
		}
		body["multicastRPs"] = rps
	}
	// Normalize the resulting request using the native reader's type/set checks.
	// Create bodies have no ID; use a local placeholder only during validation.
	candidate := map[string]any{}
	for k, v := range body {
		candidate[k] = v
	}
	if observed == nil {
		candidate["id"] = "planned"
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return nil, err
	}
	normalized, err := parseFabricMulticastVNObservation(raw)
	if err != nil || normalized.unknownFields {
		return nil, errInvalidResponse
	}
	if err := validateFabricMulticastMerged(normalized.fields); err != nil {
		return nil, err
	}
	if observed == nil {
		delete(normalized.fields, "id")
	}
	return normalized.fields, nil
}

func multicastContainsAll(next, old []string) bool {
	set := map[string]bool{}
	for _, v := range next {
		set[v] = true
	}
	for _, v := range old {
		if !set[v] {
			return false
		}
	}
	return true
}
func fabricMulticastRPKey(rp map[string]any) string {
	values := []any{rp["rpDeviceLocation"], rp["ipv4Address"], rp["ipv6Address"]}
	if rp["rpDeviceLocation"] == "FABRIC" {
		values = []any{rp["rpDeviceLocation"], rp["networkDeviceIds"]}
	}
	raw, _ := json.Marshal(values)
	return string(raw)
}

func fabricMulticastMatches(want fabricMulticastVNIntent, fabricID string, deviceIDs map[string]string, observed fabricMulticastVN) (bool, error) {
	expected, err := fabricMulticastPayload(want, fabricID, deviceIDs, &observed)
	if err != nil {
		return false, err
	}
	if !reflect.DeepEqual(expected, observed.fields) {
		return false, nil
	}

	return true, nil
}

// Validate interactions with retained fields, not just individually parsed intent.
func validateFabricMulticastMerged(fields map[string]any) error {
	ssm, _ := fields["ipv4SsmRanges"].([]string)
	rps, _ := fields["multicastRPs"].([]map[string]any)
	defaults := map[string]bool{}
	for _, rp := range rps {
		for _, flag := range []string{"isDefaultV4RP", "isDefaultV6RP"} {
			if rp[flag] == true {
				if defaults[flag] {
					return errConfigurationTransition
				}
				defaults[flag] = true
			}
		}
		asm, _ := rp["ipv4AsmRanges"].([]string)
		for _, a := range asm {
			ap, _ := netip.ParsePrefix(a)
			for _, s := range ssm {
				sp, _ := netip.ParsePrefix(s)
				if ap.Overlaps(sp) {
					return errConfigurationTransition
				}
			}
		}
	}
	return nil
}
