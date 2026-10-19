// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "testing"

func TestManagementAddressPlanningSeparatesNewIPFromIdentity(t *testing.T) {
	inventory := []Device{{ID: "switch-id", Hostname: "switch.example", Serial: "SERIAL-A", ManagementIP: "198.51.100.100", PlatformID: "C9300"}}
	ref := configurationDeviceReference{Name: "alias", FQDN: "switch.example", IP: "198.51.100.101", Serial: "SERIAL-A"}
	change, err := planManagementAddressChange(ref, inventory)
	if err != nil || change == nil || change.device.ID != "switch-id" || change.before != "198.51.100.100" || change.after != ref.IP {
		t.Fatalf("new IP incorrectly resolved as a current selector: %+v %v", change, err)
	}
	ref.IP = inventory[0].ManagementIP
	if change, err := planManagementAddressChange(ref, inventory); err != nil || change != nil {
		t.Fatal("unchanged address did not converge", err)
	}
	inventory[0].ManagementIP = "2001:db8::1"
	ref.IP = "2001:0db8:0:0:0:0:0:1"
	if change, err := planManagementAddressChange(ref, inventory); err != nil || change != nil {
		t.Fatal("equivalent IPv6 address did not converge", err)
	}
}

func TestManagementAddressPlanningRefusesUnsafeTargets(t *testing.T) {
	for _, mode := range []string{"missing-name", "wrong-serial", "missing-serial", "access-point", "legacy-access-point", "ap-family", "collision", "ambiguous-names", "duplicate-id", "invalid-old-address", "invalid-new-address", "multicast", "loopback", "mapped", "zoned"} {
		t.Run(mode, func(t *testing.T) {
			inventory := []Device{{ID: "switch-id", Hostname: "switch", Serial: "SERIAL-A", ManagementIP: "198.51.100.100", PlatformID: "C9300"}}
			ref := configurationDeviceReference{Name: "switch", IP: "198.51.100.101"}
			switch mode {
			case "missing-name":
				ref.Name = ""
			case "wrong-serial":
				ref.Serial = "OTHER"
			case "missing-serial":
				inventory[0].Serial = ""
			case "access-point":
				inventory[0].APEthernetMAC = "00:11:22:33:44:55"
			case "legacy-access-point":
				inventory[0].PlatformID = "AIR-CAP1852I"
			case "ap-family":
				inventory[0].Family = "unified ap"
			case "collision":
				inventory = append(inventory, Device{ID: "other", Hostname: "other", ManagementIP: ref.IP})
			case "ambiguous-names":
				ref.FQDN = "other"
				inventory = append(inventory, Device{ID: "other", Hostname: "other", ManagementIP: "198.51.100.103"})
			case "duplicate-id":
				inventory = append(inventory, inventory[0])
			case "invalid-old-address":
				inventory[0].ManagementIP = "not-an-address"
			case "invalid-new-address":
				ref.IP = "not-an-address"
			case "multicast":
				ref.IP = "224.0.0.1"
			case "loopback":
				ref.IP = "127.0.0.1"
			case "mapped":
				ref.IP = "::ffff:198.51.100.101"
			case "zoned":
				ref.IP = "fe80::1%eth0"
			}
			if change, err := planManagementAddressChange(ref, inventory); err == nil || change != nil {
				t.Fatal("unsafe management-address intent accepted")
			}
		})
	}
}
