// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAnycastGatewayPinnedModel(t *testing.T) {
	for _, mode := range []string{"normal", "secondary", "infra-ap", "infra-extended", "bad-pool-type", "infra-normal-flags", "normal-infra-flags", "infra-secondary", "too-many-pools", "duplicate-pool", "primary-as-secondary", "wrong-secondary-type", "bad-mss", "bad-vlan", "critical-without-auto", "auto-with-name", "bad-boolean", "unknown", "null-optionals"} {
		t.Run(mode, func(t *testing.T) {
			m := map[string]any{"l3_virtual_network": "Corp", "ip_pool_name": "Clients", "traffic_type": "DATA", "auto_generate_vlan_name": true}
			switch mode {
			case "normal":
				m["critical_pool"] = false
				m["layer2_flooding"] = true
				m["tcp_mss_adjustment"] = 1400
				m["vlan_id"] = 150
			case "secondary":
				m["additional_ip_pools"] = []any{"Second", "Third"}
			case "infra-ap":
				m["l3_virtual_network"] = "INFRA_VN"
				m["pool_type"] = "FABRIC_AP"
			case "infra-extended":
				m["l3_virtual_network"] = "INFRA_VN"
				m["pool_type"] = "EXTENDED_NODE"
				m["group_based_policy_enforcement_enabled"] = false
				m["supplicant_based_extended_node_onboarding"] = true
			case "bad-pool-type":
				m["pool_type"] = "FABRIC_AP"
			case "infra-normal-flags":
				m["l3_virtual_network"] = "INFRA_VN"
				m["pool_type"] = "FABRIC_AP"
				m["critical_pool"] = false
			case "normal-infra-flags":
				m["supplicant_based_extended_node_onboarding"] = false
			case "infra-secondary":
				m["l3_virtual_network"] = "INFRA_VN"
				m["pool_type"] = "FABRIC_AP"
				m["additional_ip_pools"] = []any{"Other"}
			case "too-many-pools":
				m["additional_ip_pools"] = []any{"a", "b", "c", "d", "e"}
			case "duplicate-pool":
				m["additional_ip_pools"] = []any{"Other", "Other"}
			case "primary-as-secondary":
				m["additional_ip_pools"] = []any{"Clients"}
			case "wrong-secondary-type":
				m["additional_ip_pools"] = []any{map[string]any{"name": "Other", "order": 2}}
			case "bad-mss":
				m["tcp_mss_adjustment"] = 1441
			case "bad-vlan":
				m["vlan_id"] = 2046
			case "critical-without-auto":
				m["auto_generate_vlan_name"] = false
				m["critical_pool"] = true
			case "auto-with-name":
				m["vlan_name"] = "Clients"
			case "bad-boolean":
				m["wireless_pool"] = "false"
			case "unknown":
				m["fabric_id"] = "injected"
			case "null-optionals":
				m["vlan_id"] = nil
				m["additional_ip_pools"] = nil
				m["wireless_pool"] = nil
			}
			before, _ := json.Marshal(m)
			got, err := parseAnycastGateway(m)
			valid := mode == "normal" || mode == "secondary" || mode == "infra-ap" || mode == "infra-extended" || mode == "null-optionals"
			if (err == nil) != valid {
				t.Fatal("unexpected parser outcome", err)
			}
			after, _ := json.Marshal(m)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("source mutated")
			}
			if !valid {
				if got.payload != nil {
					t.Fatal("partial invalid intent")
				}
				return
			}
			if mode == "normal" && (got.payload["isCriticalPool"] != false || got.payload["isLayer2FloodingEnabled"] != true || got.payload["tcpMssAdjustment"] != 1400) {
				t.Fatal("lost mapped fields")
			}
			if mode == "secondary" {
				want := []map[string]any{{"name": "Second", "order": 2}, {"name": "Third", "order": 3}}
				if !got.needsSecondaryPools || !reflect.DeepEqual(got.payload["additionalIpPools"], want) {
					t.Fatal("lost ordered pool expansion")
				}
			}
			if mode == "null-optionals" && (got.payload["vlanId"] != nil || got.payload["isWirelessPool"] != nil || got.needsSecondaryPools) {
				t.Fatal("invented optional values")
			}
		})
	}
}
