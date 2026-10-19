// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAnycastGatewayNestedModel(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "duplicate-pool", "missing-reference", "duplicate-reference", "zone-override", "null", "wrong-parent", "unknown-site", "unknown-zone", "l2-vlan-collision", "gateway-vlan-collision"} {
		t.Run(mode, func(t *testing.T) {
			var root map[string]any
			json.Unmarshal([]byte(fabricL2Fixture), &root)
			value := root["catalyst_center"].(map[string]any)["fabric"].(map[string]any)["fabric_sites"].([]any)
			site := value[0].(map[string]any)
			zone := site["fabric_zones"].([]any)[0].(map[string]any)
			gw := map[string]any{"ip_pool_name": "Pool", "l3_virtual_network": "Corp", "traffic_type": "DATA", "auto_generate_vlan_name": false, "vlan_name": "PoolVLAN", "vlan_id": 200, "additional_ip_pools": []any{"Second", "Third"}}
			site["anycast_gateways"] = []any{gw}
			zone["anycast_gateways"] = []any{"Pool"}
			switch mode {
			case "empty":
				site["anycast_gateways"] = []any{}
				zone["anycast_gateways"] = []any{}
			case "duplicate-pool":
				site["anycast_gateways"] = []any{gw, gw}
			case "missing-reference":
				zone["anycast_gateways"] = []any{"Missing"}
			case "duplicate-reference":
				zone["anycast_gateways"] = []any{"Pool", "Pool"}
			case "zone-override":
				zone["anycast_gateways"] = []any{gw}
			case "null":
				zone["anycast_gateways"] = nil
			case "wrong-parent":
				zone["name"] = "Global/Other"
			case "unknown-site":
				site["unknown"] = true
			case "unknown-zone":
				zone["unknown"] = true
			case "l2-vlan-collision":
				gw["vlan_name"] = "Clients"
			case "gateway-vlan-collision":
				site["anycast_gateways"] = []any{gw, map[string]any{"ip_pool_name": "Other", "l3_virtual_network": "Corp", "traffic_type": "DATA", "auto_generate_vlan_name": true, "vlan_id": 200}}
			}
			before, _ := json.Marshal(value)
			got, err := parseFabricSitesWithGateways(value)
			valid := mode == "valid" || mode == "empty"
			if (err == nil) != valid {
				t.Fatal("unexpected parse result", err)
			}
			if !valid && got != nil {
				t.Fatal("returned partial invalid intent")
			}
			after, _ := json.Marshal(value)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("source changed")
			}
			if !valid {
				return
			}
			if len(got.sites) != 1 || len(got.zones) != 1 || len(got.l2) != 2 {
				t.Fatal("lost existing fabric intent")
			}
			if mode == "empty" {
				if len(got.gateways) != 0 {
					t.Fatal("invented gateway")
				}
				return
			}
			if len(got.gateways) != 2 || got.gateways[0].parent != "" || got.gateways[1].parent != "Global/Lab" || got.gateways[1].path != "Global/Lab/Building" {
				t.Fatal("lost parent binding")
			}
			parent, child := got.gateways[0], got.gateways[1]
			if !reflect.DeepEqual(parent.payload, child.payload) || !child.needsSecondaryPools {
				t.Fatal("lost inherited settings/capability")
			}
			child.payload["vlanId"] = 333
			child.payload["additionalIpPools"].([]map[string]any)[0]["name"] = "Changed"
			if parent.payload["vlanId"] != 200 || parent.payload["additionalIpPools"].([]map[string]any)[0]["name"] != "Second" {
				t.Fatal("child payload aliases parent")
			}
		})
	}
}
