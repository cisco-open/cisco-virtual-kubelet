// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"testing"
)

func TestFabricSDAHandoffComposition(t *testing.T) {
	for _, mode := range []string{"valid", "mixed", "orphan", "orphan-option", "unknown", "duplicate", "wrong-layer", "unprovisioned", "zone", "bad-affinity"} {
		t.Run(mode, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal([]byte(fabricEdgeIntentModel), &root); err != nil {
				t.Fatal(err)
			}
			cc := root["catalyst_center"].(map[string]any)
			device := cc["inventory"].(map[string]any)["devices"].([]any)[0].(map[string]any)
			device["fabric_roles"] = []any{"BORDER_NODE"}
			border := map[string]any{"name": "edge", "border_types": []any{"LAYER_3"}, "local_autonomous_system_number": 65000, "default_exit": true, "import_external_routes": false, "sda_transit": "SDA", "affinity_id_prime": 0, "connected_to_internet": false}
			borders := []any{border}
			switch mode {
			case "mixed":
				border["border_types"] = []any{"LAYER_2", "LAYER_3"}
				border["l2_handoffs"] = map[string]any{"l2_without_anycast_gateway": map[string]any{"vlans": []any{map[string]any{"name": "IoT", "external_vlan": 400}}, "interfaces": []any{"GigabitEthernet1/0/1"}}}
				var ip []any
				if err := json.Unmarshal([]byte(fabricL3IPHandoffModel), &ip); err != nil {
					t.Fatal(err)
				}
				border["l3_handoffs"] = ip
			case "orphan":
				border["name"] = "missing"
			case "orphan-option":
				delete(border, "sda_transit")
			case "unknown":
				border["not_supported"] = true
			case "duplicate":
				borders = append(borders, border)
			case "wrong-layer":
				border["border_types"] = []any{"LAYER_2"}
				delete(border, "local_autonomous_system_number")
				delete(border, "default_exit")
				delete(border, "import_external_routes")
			case "unprovisioned":
				device["state"] = "ASSIGN"
			case "zone":
				device["fabric_zone"] = "Global/Lab/Building"
			case "bad-affinity":
				border["affinity_id_prime"] = -1
			}
			cc["fabric"] = map[string]any{"border_devices": borders}
			raw, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			devices, l2, l3, sda, err := parseAllFabricHandoffDeployment(raw)
			if mode != "valid" && mode != "mixed" {
				if err == nil || devices != nil || l2 != nil || l3 != nil || sda != nil {
					t.Fatal("invalid/partial intent accepted")
				}
				return
			}
			if err != nil || len(devices) != 1 || len(sda) != 1 {
				t.Fatal("missing composed intent", err)
			}
			if (len(l2) == 1 && len(l3) == 1) != (mode == "mixed") {
				t.Fatal("mixed resources lost")
			}
			got := sda[0]
			selected, selectErr := declaredFabricSDAHandoff(&configSourceSnapshot{raw: raw}, got.handoff)
			if selectErr != nil || selected == nil {
				t.Fatal("declared selection lost optional values", selectErr)
			}

			if got.device.inventory.reference.Serial != "FOC12345678" || got.device.fabricSite != "Global/Lab" || got.handoff.Device != "edge" || got.handoff.Transit != "SDA" || got.handoff.Prime == nil || *got.handoff.Prime != 0 || got.handoff.Internet == nil || *got.handoff.Internet {
				t.Fatal("lost placement/identity/options")
			}
			if _, _, _, err := parseFabricHandoffDeployment(raw); err == nil {
				t.Fatal("old compiler dropped SDA")
			}
			if _, _, err := parseFabricL2HandoffDeployment(raw); err == nil {
				t.Fatal("Layer 2 compiler dropped SDA")
			}
			if _, err := parseFabricDeviceIntents(raw); err == nil {
				t.Fatal("device compiler dropped SDA")
			}
		})
	}
}
