// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import "testing"

func TestServiceSwitchportVLANJoin(t *testing.T) {
	resources := parcelFixtures(t, "service-switchport")
	var data map[string]any
	for _, r := range resources {
		if r.Type == "service_switchport" {
			data = r.Body["data"].(map[string]any)
		}
	}
	if data == nil {
		t.Fatal("switchport parcel absent")
	}
	rows := data["interface"].([]any)
	first := rows[0].(map[string]any)
	if got := classicInput(first, "switchportTrunkAllowedVlans.value"); got != "100,200,300" {
		t.Fatalf("VLAN join: %v", got)
	}
	for _, invalid := range []any{[]any{float64(0)}, []any{float64(4095)}, []any{"100"}} {
		input := map[string]any{"name": "bad", "interfaces": []any{map[string]any{"trunk_allowed_vlans": invalid}}}
		if _, _, err := scalarParcelData("service_switchport", input); err == nil {
			t.Fatal("invalid VLAN accepted", invalid)
		}
	}
}
