// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import "testing"

func TestClassicSwitchportConversions(t *testing.T) {
	r := parcelFixtures(t, "classic-switchport")[0]
	definition := r.Body["templateDefinition"].(map[string]any)
	interfaces := classicInput(definition, "interface.vipValue").([]any)
	first := interfaces[0].(map[string]any)
	if got := classicInput(first, "switchport.trunk.allowed.vlan.vlans.vipValue"); got != "100,200,300-305" {
		t.Fatalf("joined VLANs: %v", got)
	}
	macs := classicInput(definition, "static-mac-address.vipValue").([]any)
	if got := classicInput(macs[0].(map[string]any), "macaddr.vipValue"); got != "0011.2233.4455" {
		t.Fatalf("Cisco MAC format: %v", got)
	}
	for _, bad := range []any{[]any{float64(0)}, []any{float64(4095)}, []any{"100"}} {
		input := map[string]any{"name": "bad", "description": "bad", "module_type": "4", "slot": float64(0), "sub_slot": float64(0), "interfaces": []any{map[string]any{"name": "GigabitEthernet0/0/1", "trunk_allowed_vlans": bad}}}
		if _, err := compileClassicTemplates("switchport_templates", []map[string]any{input}, nil); err == nil {
			t.Fatal("invalid VLAN accepted", bad)
		}
	}
}
