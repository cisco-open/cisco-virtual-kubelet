// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"testing"
)

func TestFabricHandoffCombinedIntent(t *testing.T) {
	for _, mode := range []string{"l3", "mixed", "collision", "unknown-border", "orphan", "not-layer3", "not-provisioned", "bad-pool", "orphan-pool", "duplicate", "unknown-network"} {
		t.Run(mode, func(t *testing.T) {
			var root map[string]any
			var handoffs []any
			if err := json.Unmarshal([]byte(fabricEdgeIntentModel), &root); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(fabricL3IPHandoffModel), &handoffs); err != nil {
				t.Fatal(err)
			}
			cc := root["catalyst_center"].(map[string]any)
			device := cc["inventory"].(map[string]any)["devices"].([]any)[0].(map[string]any)
			device["fabric_roles"] = []any{"BORDER_NODE"}
			border := map[string]any{"name": "edge", "border_types": []any{"LAYER_3"}, "local_autonomous_system_number": 65000, "default_exit": true, "import_external_routes": false, "external_handoff_pool": "Pool", "l3_handoffs": handoffs}
			borders := []any{border}
			switch mode {
			case "mixed", "collision":
				border["border_types"] = []any{"LAYER_2", "LAYER_3"}
				vlan := 400
				if mode == "collision" {
					vlan = 300
				}
				border["l2_handoffs"] = map[string]any{"l2_without_anycast_gateway": map[string]any{"vlans": []any{map[string]any{"name": "IoT", "external_vlan": vlan}}, "interfaces": []any{"GigabitEthernet1/0/1"}}}
			case "unknown-border":
				border["sda_transit"] = "NotImplemented"
			case "orphan":
				border["name"] = "missing"
			case "not-layer3":
				border["border_types"] = []any{"LAYER_2"}
				delete(border, "local_autonomous_system_number")
				delete(border, "default_exit")
				delete(border, "import_external_routes")
			case "not-provisioned":
				device["state"] = "ASSIGN"
			case "bad-pool":
				border["external_handoff_pool"] = 7
			case "orphan-pool":
				delete(border, "l3_handoffs")
			case "duplicate":
				borders = append(borders, border)
			case "unknown-network":
				iface := handoffs[0].(map[string]any)["interfaces"].([]any)[0].(map[string]any)
				iface["virtual_networks"].([]any)[0].(map[string]any)["unsupported"] = true
			}
			cc["fabric"] = map[string]any{"border_devices": borders}
			raw, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			devices, l2, l3, err := parseFabricHandoffDeployment(raw)
			if mode != "l3" && mode != "mixed" {
				if err == nil || devices != nil || l2 != nil || l3 != nil {
					t.Fatal("invalid or partial combined model accepted")
				}
				return
			}
			if err != nil || len(devices) != 1 || len(l3) != 1 || (len(l2) == 1) != (mode == "mixed") {
				t.Fatal("incomplete combined model", err)
			}
			if l3[0].handoff.pool != "Pool" || l3[0].handoff.device != "edge" || l3[0].device.inventory.reference.Serial != "FOC12345678" || l3[0].device.fabricSite != "Global/Lab" {
				t.Fatal("lost device, pool or placement identity")
			}
			if _, _, err := parseFabricL2HandoffDeployment(raw); err == nil {
				t.Fatal("L2-only compiler discarded L3 intent")
			}
			if _, err := parseFabricDeviceIntents(raw); err == nil {
				t.Fatal("device-only compiler discarded handoffs")
			}
		})
	}
}
