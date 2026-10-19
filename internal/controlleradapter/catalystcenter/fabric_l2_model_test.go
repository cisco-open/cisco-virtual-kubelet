// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFabricL2NestedModel(t *testing.T) {
	for _, mode := range []string{"valid", "auto-vlan", "null-optionals", "missing-reference", "duplicate-reference", "zone-override", "reserved-vlan", "fractional-vlan", "string-vlan", "duplicate-name", "duplicate-vlan-name", "duplicate-vlan-id", "bad-traffic", "bad-wireless", "bad-name", "unknown", "unknown-site", "unknown-zone", "null-list", "wrong-parent"} {
		t.Run(mode, func(t *testing.T) {
			vn := map[string]any{"name": "clients", "vlan_name": "Clients_10", "vlan_id": 10, "traffic_type": "DATA", "fabric_enabled_wireless": false, "associated_l3_virtual_network_name": "Corp"}
			zone := map[string]any{"name": "Global/Lab/Building", "authentication_template": map[string]any{"name": "No Authentication"}, "l2_virtual_networks": []any{"clients"}}
			site := map[string]any{"name": "Global/Lab", "authentication_template": map[string]any{"name": "Closed Authentication"}, "pub_sub_enabled": false, "l2_virtual_networks": []any{vn}, "fabric_zones": []any{zone}}
			switch mode {
			case "auto-vlan":
				delete(vn, "vlan_id")
			case "null-optionals":
				vn["vlan_id"] = nil
				vn["fabric_enabled_wireless"] = nil
				vn["associated_l3_virtual_network_name"] = nil
			case "missing-reference":
				zone["l2_virtual_networks"] = []any{"missing"}
			case "duplicate-reference":
				zone["l2_virtual_networks"] = []any{"clients", "clients"}
			case "zone-override":
				zone["l2_virtual_networks"] = []any{vn}
			case "reserved-vlan":
				vn["vlan_id"] = 2046
			case "fractional-vlan":
				vn["vlan_id"] = 10.5
			case "string-vlan":
				vn["vlan_id"] = "10"
			case "duplicate-name":
				site["l2_virtual_networks"] = []any{vn, vn}
			case "duplicate-vlan-name", "duplicate-vlan-id":
				other := map[string]any{"name": "other", "vlan_name": "Other", "vlan_id": 20, "traffic_type": "DATA"}
				if mode == "duplicate-vlan-name" {
					other["vlan_name"] = vn["vlan_name"]
				} else {
					other["vlan_id"] = vn["vlan_id"]
				}
				site["l2_virtual_networks"] = []any{vn, other}
			case "bad-traffic":
				vn["traffic_type"] = "data"
			case "bad-wireless":
				vn["fabric_enabled_wireless"] = "false"
			case "bad-name":
				vn["vlan_name"] = "Clients/10"
			case "unknown":
				vn["is_wireless_flooding_enabled"] = true
			case "unknown-site":
				site["anycast_gateways"] = []any{}
			case "unknown-zone":
				zone["anycast_gateways"] = []any{}
			case "null-list":
				site["l2_virtual_networks"] = nil
			case "wrong-parent":
				zone["name"] = "Global/Other/Building"
			}
			value := []any{site}
			before, _ := json.Marshal(value)
			sites, zones, networks, err := parseFabricSitesWithL2(value)
			valid := mode == "valid" || mode == "auto-vlan" || mode == "null-optionals"
			if (err == nil) != valid {
				t.Fatal("unexpected parse outcome", err)
			}
			if !valid && (sites != nil || zones != nil || networks != nil) {
				t.Fatal("partial invalid intent")
			}
			after, _ := json.Marshal(value)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("mutated source")
			}
			if !valid {
				return
			}
			if len(sites) != 1 || len(zones) != 1 || len(networks) != 2 || networks[0].parent != "" || networks[1].parent != "Global/Lab" || networks[1].path != "Global/Lab/Building" {
				t.Fatal("lost parent dependency")
			}
			a, b := networks[0], networks[1]
			a.path = b.path
			a.parent = b.parent
			if !reflect.DeepEqual(a, b) {
				t.Fatal("zone attributes differ from parent")
			}
			if mode == "valid" && (a.wireless == nil || *a.wireless || a.vlan != 10 || a.l3 != "Corp") {
				t.Fatal("explicit values lost")
			}
			if mode == "null-optionals" && (a.wireless != nil || a.vlan != 0 || a.l3 != "") {
				t.Fatal("invented optional defaults")
			}
		})
	}
}
func TestFabricVLANBounds(t *testing.T) {
	for _, id := range []int{-1, 0, 1, 1002, 1003, 1004, 1005, 2046, 4094, 99999} {
		if validFabricVLAN(id) {
			t.Fatalf("accepted reserved/out-of-range VLAN %d", id)
		}
	}
	for _, id := range []int{2, 1001, 1006, 2045, 2047, 4093} {
		if !validFabricVLAN(id) {
			t.Fatalf("rejected valid VLAN %d", id)
		}
	}
}
