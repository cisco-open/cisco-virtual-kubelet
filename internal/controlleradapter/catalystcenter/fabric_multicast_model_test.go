// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func multicastVNFixture() map[string]any {
	return map[string]any{"name": "Corp", "ip_pool_name": "multicast", "ipv4_ssm_ranges": []any{"232.0.0.0/8"}, "multicast_rps": []any{
		map[string]any{"name": "external", "rp_location": "EXTERNAL", "ipv4_address": "192.0.2.1", "ipv4_asm_ranges": []any{"239.0.0.0/8"}, "is_default_v4_rp": false},
		map[string]any{"name": "fabric", "rp_location": "FABRIC", "fabric_rps": []any{"border-b", "border-a"}, "ipv6_asm_ranges": []any{"ff3e::/96"}, "is_default_v6_rp": true},
	}}
}
func TestFabricMulticastModel(t *testing.T) {
	for _, mode := range []string{"valid", "null-optionals", "empty", "false", "duplicate-vn", "unknown-field", "native-id-injection", "invalid-replication", "invalid-wireless", "duplicate-label", "two-defaults", "overlap", "wrong-v4-family", "wrong-v6-family", "external-no-address", "external-two-addresses", "external-wrong-ranges", "external-wrong-default", "fabric-no-device", "fabric-three-devices", "fabric-suppressed-address", "unknown-rp-field", "missing-pool", "missing-vn", "unknown-vn-field"} {
		t.Run(mode, func(t *testing.T) {
			vn := multicastVNFixture()
			rps := vn["multicast_rps"].([]any)
			external := rps[0].(map[string]any)
			fabric := rps[1].(map[string]any)
			model := map[string]any{"replication_mode": "NATIVE_MULTICAST", "wireless_multicast_enabled": true, "virtual_networks": []any{vn}}
			switch mode {
			case "null-optionals":
				model = map[string]any{"replication_mode": nil, "wireless_multicast_enabled": nil, "virtual_networks": nil}
			case "empty":
				model = map[string]any{}
			case "false":
				model["wireless_multicast_enabled"] = false
			case "duplicate-vn":
				model["virtual_networks"] = []any{vn, vn}
			case "unknown-field":
				model["extra"] = nil
			case "native-id-injection":
				model["fabricId"] = "injected"
			case "invalid-replication":
				model["replication_mode"] = "native"
			case "invalid-wireless":
				model["wireless_multicast_enabled"] = "false"
			case "duplicate-label":
				fabric["name"] = "external"
			case "two-defaults":
				external["is_default_v4_rp"] = true
				fabric["is_default_v4_rp"] = true
			case "overlap":
				external["ipv4_asm_ranges"] = []any{"232.1.0.0/16"}
			case "wrong-v4-family":
				external["ipv4_address"] = "2001:db8::1"
			case "wrong-v6-family":
				delete(external, "ipv4_address")
				external["ipv6_address"] = "192.0.2.1"
			case "external-no-address":
				delete(external, "ipv4_address")
			case "external-two-addresses":
				external["ipv6_address"] = "2001:db8::1"
			case "external-wrong-ranges":
				external["ipv6_asm_ranges"] = []any{"ff3e::/96"}
			case "external-wrong-default":
				external["is_default_v6_rp"] = true
			case "fabric-no-device":
				delete(fabric, "fabric_rps")
			case "fabric-three-devices":
				fabric["fabric_rps"] = []any{"a", "b", "c"}
			case "fabric-suppressed-address":
				fabric["ipv4_address"] = "not-a-native-address"
				fabric["ipv6_address"] = "ignored"
			case "unknown-rp-field":
				fabric["networkDeviceIds"] = []any{"injected"}
			case "missing-pool":
				delete(vn, "ip_pool_name")
			case "missing-vn":
				delete(vn, "name")
			case "unknown-vn-field":
				vn["fabricId"] = "injected"
			}
			before, _ := json.Marshal(model)
			got, err := parseFabricMulticast(model)
			valid := mode == "valid" || mode == "null-optionals" || mode == "empty" || mode == "false" || mode == "fabric-suppressed-address"
			if (err == nil) != valid {
				t.Fatal("unexpected parser result", err)
			}
			after, _ := json.Marshal(model)
			if string(before) != string(after) {
				t.Fatal("input modified")
			}
			if !valid {
				if !reflect.DeepEqual(got, fabricMulticastIntent{}) {
					t.Fatal("partial invalid intent")
				}
				return
			}
			if mode == "null-optionals" || mode == "empty" {
				if got.wireless != nil || got.replication != "" || len(got.networks) != 0 {
					t.Fatal("invented defaults")
				}
				return
			}
			if got.replication != "NATIVE_MULTICAST" || got.wireless == nil || *got.wireless != (mode != "false") || len(got.networks) != 1 {
				t.Fatal("top-level mapping")
			}
			n := got.networks[0]
			if n.name != "Corp" || n.pool != "multicast" || n.rps[0].payload["isDefaultV4RP"] != false {
				t.Fatal("lost VN or false flag")
			}
			if !reflect.DeepEqual(n.rps[1].devices, []string{"border-a", "border-b"}) {
				t.Fatal("unstable RP references")
			}
			if n.rps[1].payload["ipv4Address"] != nil || n.rps[1].payload["ipv6Address"] != nil || n.rps[1].payload["networkDeviceIds"] != nil || n.rps[1].payload["name"] != nil {
				t.Fatal("unresolved or model-only identity leaked")
			}
		})
	}
}
func TestMulticastRanges(t *testing.T) {
	cases := []struct {
		value          string
		v4, ssm, valid bool
	}{
		{"225.0.0.0/8", true, true, true}, {"239.0.0.0/8", true, true, true}, {"232.1.0.0/16", true, true, true},
		{"224.0.0.0/4", true, false, true}, {"224.0.0.0/4", true, true, false}, {"224.0.0.0/8", true, true, false},
		{"240.0.0.0/8", true, true, false}, {"192.0.2.0/24", true, false, false}, {"232.1.0.1/16", true, true, false},
		{"ff3e::/96", false, false, true}, {"ff00::/8", false, false, true}, {"fe00::/7", false, false, false},
		{"2001:db8::/32", false, false, false}, {"ff3e::1/96", false, false, false}, {"ff3e::/96", true, false, false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s-v4=%t-ssm=%t", tc.value, tc.v4, tc.ssm), func(t *testing.T) {
			got, err := multicastRanges([]any{tc.value, tc.value}, tc.v4, tc.ssm)
			if (err == nil) != tc.valid {
				t.Fatal("unexpected range result", err)
			}
			if tc.valid && len(got) != 1 {
				t.Fatal("set duplicate retained")
			}
			if !tc.valid && got != nil {
				t.Fatal("partial invalid set")
			}
		})
	}
}

func TestFabricMulticastRPIntentPresence(t *testing.T) {
	for _, mode := range []string{"omitted", "null", "empty"} {
		t.Run(mode, func(t *testing.T) {
			model := map[string]any{"name": "Corp", "ip_pool_name": "multicast"}
			if mode == "null" {
				model["multicast_rps"] = nil
			}
			if mode == "empty" {
				model["multicast_rps"] = []any{}
			}
			got, err := parseFabricMulticastVN(model)
			if err != nil || got.rpsDeclared != (mode == "empty") || len(got.rps) != 0 {
				t.Fatal("lost RP declaration presence", err)
			}
		})
	}
}
