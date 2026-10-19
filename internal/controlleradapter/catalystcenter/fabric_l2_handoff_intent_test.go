// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"testing"
)

func TestFabricL2HandoffDeploymentIntent(t *testing.T) {
	for _, mode := range []string{"valid", "unknown-border", "orphan", "not-layer2", "not-provisioned", "duplicate", "bad-handoff"} {
		t.Run(mode, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal([]byte(fabricEdgeIntentModel), &root); err != nil {
				t.Fatal(err)
			}
			cc := root["catalyst_center"].(map[string]any)
			device := cc["inventory"].(map[string]any)["devices"].([]any)[0].(map[string]any)
			device["fabric_roles"] = []any{"BORDER_NODE"}
			border := map[string]any{"name": "edge", "border_types": []any{"LAYER_2"}, "l2_handoffs": map[string]any{"l2_without_anycast_gateway": map[string]any{"vlans": []any{map[string]any{"name": "IoT", "external_vlan": 400}}, "interfaces": []any{"GigabitEthernet1/0/1"}}}}
			borders := []any{border}
			switch mode {
			case "unknown-border":
				border["unsupported"] = true
			case "orphan":
				border["name"] = "other"
			case "not-layer2":
				border["border_types"] = []any{"LAYER_3"}
				border["local_autonomous_system_number"] = 65001
				border["default_exit"] = true
				border["import_external_routes"] = false
			case "not-provisioned":
				device["state"] = "ASSIGN"
			case "duplicate":
				borders = append(borders, border)
			case "bad-handoff":
				border["l2_handoffs"] = map[string]any{"unknown": true}
			}
			cc["fabric"] = map[string]any{"border_devices": borders}
			raw, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			devices, handoffs, err := parseFabricL2HandoffDeployment(raw)
			if (err == nil) != (mode == "valid") {
				t.Fatal(mode, err)
			}
			if mode == "valid" {
				if len(devices) != 1 || len(handoffs) != 1 || handoffs[0].device.fabricSite != "Global/Lab" || handoffs[0].device.inventory.reference.Serial != "FOC12345678" || handoffs[0].handoff.externalVLAN != 400 {
					t.Fatal("lost inventory/site binding")
				}
				if _, err := parseFabricDeviceIntents(raw); err == nil {
					t.Fatal("device-only parser silently discarded handoff")
				}
			}
		})
	}
}
