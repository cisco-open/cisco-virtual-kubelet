// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "reflect"

// Compare declared attributes only, but require positive evidence for every
// declared value. The API does not return autoGenerateVlanName; verify its
// documented resulting name using bound reservation data instead.
func anycastGatewayMatches(want anycastGatewayIntent, got anycastGateway, pools anycastPoolDependencies, parent *anycastGateway) (bool, error) {
	if got.unknownFields {
		return false, errConfigurationTransition
	}
	if !validAPIID(got.ID) || !validAPIID(got.FabricID) || !validFabricVLAN(got.VLAN) || pools.primary.Name != want.pool || !validAPIID(pools.primary.ID) || pools.digest == "" {
		return false, errInvalidResponse
	}
	if got.Pool != want.pool || got.Network != want.network {
		return false, nil
	}
	if parent != nil {
		if !validAPIID(parent.ID) || !validFabricVLAN(parent.VLAN) || parent.Pool != want.pool || parent.Network != want.network || parent.unknownFields {
			return false, errConfigurationStale
		}
		if got.VLAN != parent.VLAN || got.VLANName != parent.VLANName {
			return false, nil
		}
	}
	if want.needsSecondaryPools {
		expected, ok := want.payload["additionalIpPools"].([]map[string]any)
		if !ok || len(expected) != len(pools.secondary) {
			return false, errInvalidResponse
		}
		for i, pool := range pools.secondary {
			if !validAPIID(pool.ID) || pool.Name != expected[i]["name"] || pool.SiteID != pools.primary.SiteID {
				return false, errConfigurationStale
			}
		}
	}
	for key, value := range want.payload {
		if key == "autoGenerateVlanName" {
			continue
		}
		observed, present := got.fields[key]
		if !present {
			return false, errInvalidResponse
		}
		if key == "additionalIpPools" && !got.secondaryKnown {
			return false, errInvalidResponse
		}
		if !reflect.DeepEqual(value, observed) {
			return false, nil
		}
	}
	auto, ok := want.payload["autoGenerateVlanName"].(bool)
	if !ok {
		return false, errInvalidResponse
	}
	if auto {
		name := ""
		critical := false
		if want.network != "INFRA_VN" {
			var known bool
			critical, known = got.fields["isCriticalPool"].(bool)
			if !known {
				return false, errInvalidResponse
			}
		}
		var err error
		name, err = generatedAnycastVLANName(want.network, critical, got.fields["trafficType"], pools)
		if err != nil {
			return false, err
		}
		if got.VLANName != name {
			return false, nil
		}
	}
	return true, nil
}

// Use the same naming rule for preflight conflict checks and native readback.
func generatedAnycastVLANName(network string, critical bool, traffic any, pools anycastPoolDependencies) (string, error) {
	if critical {
		switch traffic {
		case "DATA":
			return "CRITICAL_VLAN", nil
		case "VOICE":
			return "VOICE_VLAN", nil
		default:
			return "", errInvalidResponse
		}
	}
	prefix, err := poolPrefix(pools.primary.V4)
	if err != nil || !prefix.Addr().Is4() {
		return "", errInvalidResponse
	}
	return prefix.String() + "-" + network, nil
}
