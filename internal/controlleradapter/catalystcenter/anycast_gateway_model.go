// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

// Anycast gateway attributes follow the pinned module and provider. Fabric and
// IP-pool identities are deliberately resolved later, never accepted from YAML.
type anycastGatewayIntent struct {
	pool, network       string
	payload             map[string]any
	needsSecondaryPools bool
}

func parseAnycastGateway(value any) (anycastGatewayIntent, error) {
	m, ok := value.(map[string]any)
	if !ok {
		return anycastGatewayIntent{}, errInvalidResponse
	}
	out := anycastGatewayIntent{payload: map[string]any{}}
	texts := map[string]string{"l3_virtual_network": "virtualNetworkName", "ip_pool_name": "ipPoolName", "vlan_name": "vlanName", "traffic_type": "trafficType", "pool_type": "poolType", "security_group_name": "securityGroupName"}
	flags := map[string]string{"critical_pool": "isCriticalPool", "intra_subnet_routing_enabled": "isIntraSubnetRoutingEnabled", "ip_directed_broadcast": "isIpDirectedBroadcast", "layer2_flooding": "isLayer2FloodingEnabled", "multiple_ip_to_mac_addresses": "isMultipleIpToMacAddresses", "wireless_pool": "isWirelessPool", "auto_generate_vlan_name": "autoGenerateVlanName", "supplicant_based_extended_node_onboarding": "isSupplicantBasedExtendedNodeOnboarding", "group_based_policy_enforcement_enabled": "isGroupBasedPolicyEnforcementEnabled"}
	for k, v := range m {
		if target, exists := texts[k]; exists {
			if v == nil {
				continue
			}
			s, ok := v.(string)
			if !ok || !validProfileName(s) {
				return anycastGatewayIntent{}, errInvalidResponse
			}
			out.payload[target] = s
		} else if target, exists := flags[k]; exists {
			if v == nil {
				continue
			}
			b, ok := v.(bool)
			if !ok {
				return anycastGatewayIntent{}, errInvalidResponse
			}
			out.payload[target] = b
		} else {
			switch k {
			case "vlan_id":
				if v == nil {
					continue
				}
				n, err := plannedAPNumber(v, true)
				if err != nil || n < 2 || n > 4093 || !validFabricVLAN(int(n)) {
					return anycastGatewayIntent{}, errInvalidResponse
				}
				out.payload["vlanId"] = int(n)
			case "tcp_mss_adjustment":
				if v == nil {
					continue
				}
				n, err := plannedAPNumber(v, true)
				if err != nil || n < 500 || n > 1440 {
					return anycastGatewayIntent{}, errInvalidResponse
				}
				out.payload["tcpMssAdjustment"] = int(n)
			case "additional_ip_pools": // Validate once the primary pool is known.
			default:
				return anycastGatewayIntent{}, errInvalidResponse
			}
		}
	}
	out.pool, _ = out.payload["ipPoolName"].(string)
	out.network, _ = out.payload["virtualNetworkName"].(string)
	if out.pool == "" || out.network == "" {
		return anycastGatewayIntent{}, errInvalidResponse
	}
	traffic, _ := out.payload["trafficType"].(string)
	if traffic != "DATA" && traffic != "VOICE" {
		return anycastGatewayIntent{}, errInvalidResponse
	}
	auto, ok := out.payload["autoGenerateVlanName"].(bool)
	if !ok {
		return anycastGatewayIntent{}, errInvalidResponse
	}
	if auto && out.payload["vlanName"] != nil {
		return anycastGatewayIntent{}, errInvalidResponse
	}
	if out.payload["isCriticalPool"] == true && !auto {
		return anycastGatewayIntent{}, errInvalidResponse
	}
	infra := out.network == "INFRA_VN"
	poolType, _ := out.payload["poolType"].(string)
	if infra {
		if poolType != "FABRIC_AP" && poolType != "EXTENDED_NODE" {
			return anycastGatewayIntent{}, errInvalidResponse
		}
		for _, k := range []string{"securityGroupName", "isCriticalPool", "isIntraSubnetRoutingEnabled", "isIpDirectedBroadcast", "isLayer2FloodingEnabled", "isMultipleIpToMacAddresses", "isWirelessPool"} {
			if out.payload[k] != nil {
				return anycastGatewayIntent{}, errInvalidResponse
			}
		}
	} else {
		if poolType != "" || out.payload["isSupplicantBasedExtendedNodeOnboarding"] != nil || out.payload["isGroupBasedPolicyEnforcementEnabled"] != nil {
			return anycastGatewayIntent{}, errInvalidResponse
		}
	}
	if poolType != "EXTENDED_NODE" && out.payload["isGroupBasedPolicyEnforcementEnabled"] != nil {
		return anycastGatewayIntent{}, errInvalidResponse
	}
	if v := m["additional_ip_pools"]; v != nil {
		list, ok := v.([]any)
		if !ok || len(list) > 4 || infra && len(list) > 0 {
			return anycastGatewayIntent{}, errInvalidResponse
		}
		pools := []map[string]any{}
		seen := map[string]bool{out.pool: true}
		for i, v := range list {
			s, ok := v.(string)
			if !ok || !validProfileName(s) || seen[s] {
				return anycastGatewayIntent{}, errInvalidResponse
			}
			seen[s] = true
			pools = append(pools, map[string]any{"name": s, "order": i + 2})
		}
		if len(pools) > 0 {
			out.payload["additionalIpPools"] = pools
			out.needsSecondaryPools = true
		}
	}
	return out, nil
}
