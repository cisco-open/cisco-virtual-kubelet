// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const fabricL3IPHandoffModel = `[{"name":"Transit","interfaces":[{"name":"GigabitEthernet1/0/1","virtual_networks":[{"name":"Users","vlan":300,"local_ip_address":"192.0.2.1/30","peer_ip_address":"192.0.2.2/30","local_ipv6_address":"2001:db8::1/64","peer_ipv6_address":"2001:db8::2/64","tcp_mss_adjustment":1400}]}]}]`

func TestFabricL3IPHandoffModel(t *testing.T) {
	for _, tc := range []struct{ name, from, to string }{
		{"valid", "", ""},
		{"unknown-transit", `"interfaces":`, `"unknown":true,"interfaces":`},
		{"unknown-interface", `"virtual_networks":`, `"unknown":true,"virtual_networks":`},
		{"unknown-network", `"vlan":300`, `"vlan":300,"unknown":true`},
		{"invalid-vlan", `"vlan":300`, `"vlan":4094`},
		{"reserved-internal-vlan", `"vlan":300`, `"vlan":2046`},
		{"legacy-reserved-vlan", `"vlan":300`, `"vlan":1002`},
		{"reserved-vlan", `"vlan":300`, `"vlan":1`},
		{"fractional-vlan", `"vlan":300`, `"vlan":300.5`},
		{"low-mss", `:1400`, `:499`},
		{"high-mss", `:1400`, `:1441`},
		{"v4-wrong-family", `192.0.2.1/30`, `2001:db8::1/64`},
		{"v6-wrong-family", `2001:db8::1/64`, `192.0.2.1/30`},
		{"mapped-ipv6", `2001:db8::1/64`, `::ffff:192.0.2.1/120`},
		{"no-prefix", `192.0.2.1/30`, `192.0.2.1`},
		{"bad-prefix", `192.0.2.1/30`, `192.0.2.1/33`},
		{"multicast", `192.0.2.1/30`, `224.0.0.1/24`},
		{"empty-interface", `GigabitEthernet1/0/1`, ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := fabricL3IPHandoffModel
			if tc.from != "" {
				raw = strings.Replace(raw, tc.from, tc.to, 1)
			}
			var value any
			if err := json.Unmarshal([]byte(raw), &value); err != nil {
				t.Fatal(err)
			}
			rows, err := parseFabricL3IPHandoffs("border", "", value)
			if tc.name != "valid" {
				if err == nil || rows != nil {
					t.Fatal("invalid or partial model accepted")
				}
				return
			}
			if err != nil || len(rows) != 1 || rows[0].local4 != "192.0.2.1/30" || rows[0].peer6 != "2001:db8::2/64" || rows[0].mss == nil || *rows[0].mss != 1400 {
				t.Fatal("lost host address or optional field", rows, err)
			}
			pooled, err := parseFabricL3IPHandoffs("border", "HandoffPool", value)
			if err != nil || pooled[0].pool != "HandoffPool" || pooled[0].local4 != rows[0].local4 {
				t.Fatal("pool selection silently discarded declared data", err)
			}
		})
	}
}

func TestFabricL3IPHandoffExpansion(t *testing.T) {
	for _, mode := range []string{"valid", "duplicate-network", "duplicate-tag", "duplicate-port", "duplicate-transit", "limit"} {
		t.Run(mode, func(t *testing.T) {
			rows := []any{map[string]any{"name": "Zebra", "vlan": 301}, map[string]any{"name": "Alpha", "vlan": 302}}
			switch mode {
			case "duplicate-network":
				rows[1].(map[string]any)["name"] = "Zebra"
			case "duplicate-tag":
				rows[1].(map[string]any)["vlan"] = 301
			case "limit":
				rows = nil
				for i := 0; i < 1001; i++ {
					rows = append(rows, map[string]any{"name": fmt.Sprintf("VN%d", i), "vlan": i + 2100})
				}
			}
			ports := []any{map[string]any{"name": "GigabitEthernet1/0/1", "virtual_networks": rows}}
			if mode == "duplicate-port" {
				ports = append(ports, ports[0])
			}
			value := []any{map[string]any{"name": "Transit", "interfaces": ports}}
			if mode == "duplicate-transit" {
				value = append(value, value[0])
			}
			got, err := parseFabricL3IPHandoffs("border", "Pool", value)
			if mode == "valid" {
				if err != nil || len(got) != 2 || got[0].network != "Alpha" || got[0].mss != nil || got[0].local4 != "" {
					t.Fatal("incorrect canonical expansion", got, err)
				}
			} else if err == nil || got != nil {
				t.Fatal("conflict or oversized expansion accepted")
			}
		})
	}
}
