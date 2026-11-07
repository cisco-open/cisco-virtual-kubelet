// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import "testing"

func TestClassicSVIConditionalAndNestedRows(t *testing.T) {
	r := parcelFixtures(t, "classic-svi")[0]
	data := r.Body["templateDefinition"].(map[string]any)
	for _, path := range []string{"access-list.vipValue", "ipv6.access-list.vipValue"} {
		rows, ok := classicInput(data, path).([]any)
		if !ok || len(rows) != 1 {
			t.Fatalf("%s missing: %v", path, classicInput(data, path))
		}
	}
	if got := classicInput(data, "ipv6-vrrp.vipValue"); got == nil {
		t.Fatal("IPv6 VRRP tree missing")
	}
	for _, raw := range []any{"2001:db8::1/129", "192.0.2.1/24", "bad"} {
		_, err := compileClassicTemplates("svi_interface_templates", []map[string]any{{"name": "bad", "description": "bad", "ipv6_address": raw}}, nil)
		if err == nil {
			t.Fatal("invalid IPv6 SVI address accepted", raw)
		}
	}
}

func TestClassicCellularInterfaceConditionalRows(t *testing.T) {
	r := parcelFixtures(t, "classic-cellular-interface")[0]
	data := r.Body["templateDefinition"].(map[string]any)
	for path, want := range map[string]int{
		"tunnel-interface.encapsulation.vipValue": 2,
		"policer.vipValue":                        1,
		"access-list.vipValue":                    1,
		"ipv6.access-list.vipValue":               1,
	} {
		rows, ok := classicInput(data, path).([]any)
		if !ok || len(rows) != want {
			t.Fatalf("%s: got %v, want %d rows", path, classicInput(data, path), want)
		}
	}
}
