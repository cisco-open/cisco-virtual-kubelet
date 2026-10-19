// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestLANLinkPhysicalIdentity(t *testing.T) {
	wants, err := parseLANLinks(lanLinkModel(t, lanLinkFixture()))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "missing", "ip-alias", "serial-alias", "no-serial", "access-point"} {
		t.Run(mode, func(t *testing.T) {
			inventory := []Device{
				{ID: "seed-id", ManagementIP: "192.0.2.10", Serial: "FOC12345678"},
				{ID: "peer-id", ManagementIP: "192.0.2.11", Serial: "FOC87654321"},
			}
			switch mode {
			case "missing":
				inventory = inventory[:1]
			case "ip-alias":
				inventory = append(inventory, Device{ID: "alias", ManagementIP: "192.0.2.11", Serial: "FOC00000000"})
			case "serial-alias":
				inventory = append(inventory, Device{ID: "alias", ManagementIP: "192.0.2.12", Serial: "foc87654321"})
			case "no-serial":
				inventory[1].Serial = ""
			case "access-point":
				inventory[1].PlatformID = "C9130AXI"
			}
			devices, err := bindLANLinkDevices(wants[0], inventory)
			if (err == nil) != (mode == "valid") {
				t.Fatal(devices, err)
			}
			if err == nil && (len(devices) != 2 || devices[0].ID != "peer-id" || devices[0].Serial != "foc87654321") {
				t.Fatal("missing canonical identity", devices)
			}
		})
	}
}
