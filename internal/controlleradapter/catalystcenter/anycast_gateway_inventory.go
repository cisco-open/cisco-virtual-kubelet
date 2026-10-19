// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
)

const anycastGatewaysPath = "/dna/intent/api/v1/sda/anycastGateways"

type anycastGatewayKey struct{ fabricID, pool string }
type anycastGateway struct {
	ID, FabricID, Network, Pool, VLANName string
	VLAN                                  int
	fields                                map[string]any
	secondaryKnown, unknownFields         bool
}

func parseAnycastGatewayObservation(raw json.RawMessage) (anycastGateway, error) {
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return anycastGateway{}, errInvalidResponse
	}
	out := anycastGateway{fields: map[string]any{}}
	for k, v := range fields {
		switch k {
		case "id", "fabricId", "virtualNetworkName", "ipPoolName", "vlanName", "trafficType":
			s, ok := v.(string)
			if !ok || !validProfileName(s) {
				return anycastGateway{}, errInvalidResponse
			}
			out.fields[k] = s
		case "poolType", "securityGroupName":
			if v == nil {
				continue
			}
			s, ok := v.(string)
			if !ok || s != "" && !validProfileName(s) {
				return anycastGateway{}, errInvalidResponse
			}
			out.fields[k] = s
		case "vlanId", "tcpMssAdjustment":
			if v == nil && k == "tcpMssAdjustment" {
				continue
			}
			n, err := plannedAPNumber(v, true)
			if err != nil {
				return anycastGateway{}, errInvalidResponse
			}
			if k == "vlanId" && (n < 2 || n > 4093 || !validFabricVLAN(int(n))) {
				return anycastGateway{}, errInvalidResponse
			}
			// Preserve a reported zero without assigning it disabled/default semantics.
			// Desired nonzero MSS remains 500–1440.
			if k == "tcpMssAdjustment" && (n != 0 && (n < 500 || n > 1440)) {
				return anycastGateway{}, errInvalidResponse
			}
			out.fields[k] = int(n)
		case "isCriticalPool", "isLayer2FloodingEnabled", "isWirelessPool", "isWirelessFloodingEnabled", "isResourceGuardEnabled", "isIpDirectedBroadcast", "isIntraSubnetRoutingEnabled", "isMultipleIpToMacAddresses", "isSupplicantBasedExtendedNodeOnboarding", "isGroupBasedPolicyEnforcementEnabled":
			if v == nil {
				continue
			}
			b, ok := v.(bool)
			if !ok {
				return anycastGateway{}, errInvalidResponse
			}
			out.fields[k] = b
		case "additionalIpPools": // Parsed after primary-pool identity.
		default:
			out.unknownFields = true
		}
	}
	out.ID, _ = out.fields["id"].(string)
	out.FabricID, _ = out.fields["fabricId"].(string)
	out.Network, _ = out.fields["virtualNetworkName"].(string)
	out.Pool, _ = out.fields["ipPoolName"].(string)
	out.VLANName, _ = out.fields["vlanName"].(string)
	out.VLAN, _ = out.fields["vlanId"].(int)
	traffic, _ := out.fields["trafficType"].(string)
	if !validAPIID(out.ID) || !validAPIID(out.FabricID) || out.Network == "" || out.Pool == "" || out.VLANName == "" || !validFabricVLAN(out.VLAN) || (traffic != "DATA" && traffic != "VOICE") {
		return anycastGateway{}, errInvalidResponse
	}
	poolType, _ := out.fields["poolType"].(string)
	if out.Network == "INFRA_VN" {
		if poolType != "FABRIC_AP" && poolType != "EXTENDED_NODE" {
			return anycastGateway{}, errInvalidResponse
		}
	} else if poolType != "" {
		return anycastGateway{}, errInvalidResponse
	}
	if value, present := fields["additionalIpPools"]; present && value != nil {
		list, ok := value.([]any)
		if !ok || len(list) > 4 {
			return anycastGateway{}, errInvalidResponse
		}
		pools := []map[string]any{}
		names := map[string]bool{out.Pool: true}
		orders := map[int]bool{}
		for _, v := range list {
			m, ok := v.(map[string]any)
			if !ok {
				return anycastGateway{}, errInvalidResponse
			}
			name, ok := m["name"].(string)
			if !ok || !validProfileName(name) || names[name] {
				return anycastGateway{}, errInvalidResponse
			}
			n, err := plannedAPNumber(m["order"], true)
			if err != nil || n < 2 || n > 5 || orders[int(n)] {
				return anycastGateway{}, errInvalidResponse
			}
			for k := range m {
				if k != "name" && k != "order" {
					out.unknownFields = true
				}
			}
			names[name] = true
			orders[int(n)] = true
			pools = append(pools, map[string]any{"name": name, "order": int(n)})
		}
		if out.Network == "INFRA_VN" && len(pools) > 0 {
			return anycastGateway{}, errInvalidResponse
		}
		sort.Slice(pools, func(i, j int) bool { return pools[i]["order"].(int) < pools[j]["order"].(int) })
		out.fields["additionalIpPools"] = pools
		out.secondaryKnown = true
	}
	return out, nil
}
func (c *client) anycastGateways(ctx context.Context) (map[anycastGatewayKey]anycastGateway, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	rows, err := listInventoryPagesWithGet(ctx, get, anycastGatewaysPath, nil, func(raw json.RawMessage) string {
		var identity struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &identity) != nil {
			return ""
		}
		return identity.ID
	}, 50)
	if err != nil {
		return nil, err
	}
	out := map[anycastGatewayKey]anycastGateway{}
	type vlanKey struct {
		fabric string
		id     int
	}
	type nameKey struct{ fabric, name string }
	vlans := map[vlanKey]bool{}
	names := map[nameKey]bool{}
	for _, raw := range rows {
		n, err := parseAnycastGatewayObservation(raw)
		if err != nil {
			return nil, err
		}
		key := anycastGatewayKey{n.FabricID, n.Pool}
		vk, nk := vlanKey{n.FabricID, n.VLAN}, nameKey{n.FabricID, n.VLANName}
		if _, exists := out[key]; exists || vlans[vk] || names[nk] {
			return nil, errInvalidResponse
		}
		vlans[vk] = true
		names[nk] = true
		out[key] = n
	}
	return out, nil
}
