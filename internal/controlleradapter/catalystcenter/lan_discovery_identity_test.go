// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"testing"
)

func TestLANDiscoveryPhysicalScope(t *testing.T) {
	for _, mode := range []string{"single", "managed-stack", "missing", "unbounded", "managed", "duplicate", "stack-complete", "stack-partial", "stack-alias"} {
		t.Run(mode, func(t *testing.T) {
			wants, _, err := parseLANSessionIntents([]byte(lanSessionModelFixture()), false)
			if err != nil {
				t.Fatal(err)
			}
			want := wants[0]
			row := pnpFixture("pnp-id", "FOC12345678")
			if mode == "stack-complete" || mode == "stack-partial" || mode == "stack-alias" {
				row["deviceInfo"].(map[string]any)["stackInfo"] = map[string]any{"stackMemberList": []any{map[string]any{"serialNumber": "FOC87654321", "pid": "C9300-48P"}}}
				if mode != "stack-partial" {
					want.Devices = append(want.Devices, lanDiscoveryIntent{Serial: "FOC87654321", Site: "Global/Lab/Building"})
				}
			}
			raw, _ := json.Marshal(row)
			var device pnpDevice
			if json.Unmarshal(raw, &device) != nil {
				t.Fatal("fixture")
			}
			pnp := []pnpDevice{device}
			inventory := []Device{}
			switch mode {
			case "missing":
				pnp = nil
			case "unbounded":
				want.Devices = nil
			case "managed-stack":
				inventory = append(inventory, Device{ID: "inventory-id", Serial: "FOC87654321,foc12345678"})
			case "managed":
				inventory = append(inventory, Device{ID: "inventory-id", Serial: "foc12345678"})
			case "duplicate":
				pnp = append(pnp, device)
			case "stack-alias":
				pnp = append(pnp, pnpDevice{ID: "other", Info: &pnpDeviceInfo{Serial: "FOC87654321", PID: "C9300-48P"}})
			}
			got, err := bindLANDiscoveryDevices(want, pnp, inventory)
			valid := mode == "single" || mode == "stack-complete"
			if (err == nil) != valid {
				t.Fatal("wrong scope outcome", err)
			}
			if valid && (len(got) != 1 || got[0].ID != "pnp-id") {
				t.Fatal("lost PnP identity")
			}
		})
	}
}
