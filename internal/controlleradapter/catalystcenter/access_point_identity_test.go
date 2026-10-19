// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAccessPointConfigurationIdentity(t *testing.T) {
	for _, mode := range []string{"valid", "old-family", "radio-only", "missing-serial", "ip-only", "wrong-type", "duplicate-mac", "duplicate-serial", "conflicting-name", "multicast", "zero", "eui64", "rename"} {
		t.Run(mode, func(t *testing.T) {
			var ap Device
			if err := json.Unmarshal([]byte(`{"id":"ap-id","hostname":"old-name","serialNumber":"SERIAL","platformId":"C9130AXI","family":"Unified AP","managementIpAddress":"192.0.2.4","macAddress":"02:00:00:00:00:02","apEthernetMacAddress":"02-00-00-00-00-01"}`), &ap); err != nil {
				t.Fatal(err)
			}
			ref := configurationDeviceReference{Name: "new-name", Serial: "SERIAL"}
			inventory := []Device{ap}
			switch mode {
			case "old-family":
				inventory[0].PlatformID = "AIR-AP3802I"
			case "radio-only":
				inventory[0].APEthernetMAC = ""
			case "missing-serial":
				ref.Serial = ""
				ref.Name = "old-name"
			case "ip-only":
				ref = configurationDeviceReference{IP: "192.0.2.4"}
			case "wrong-type":
				inventory[0].PlatformID = "C9300"
				inventory[0].Family = "Switches and Hubs"
			case "duplicate-mac":
				other := ap
				other.ID = "other"
				other.Serial = "OTHER"
				other.Hostname = "other"
				other.APEthernetMAC = "02:00:00:00:00:01"
				inventory = append(inventory, other)
			case "duplicate-serial":
				other := ap
				other.ID = "other"
				other.Hostname = "other"
				other.APEthernetMAC = "02:00:00:00:00:03"
				inventory = append(inventory, other)
			case "conflicting-name":
				other := ap
				other.ID = "other"
				other.Serial = "OTHER"
				other.Hostname = "new-name"
				other.APEthernetMAC = "02:00:00:00:00:03"
				inventory = append(inventory, other)
			case "multicast":
				inventory[0].APEthernetMAC = "01:00:00:00:00:01"
			case "zero":
				inventory[0].APEthernetMAC = "00:00:00:00:00:00"
			case "eui64":
				inventory[0].APEthernetMAC = "02:00:00:00:00:00:00:01"
			}
			before, _ := json.Marshal(inventory)
			target, err := resolveAccessPointConfigurationTarget(ref, inventory)
			valid := mode == "valid" || mode == "old-family" || mode == "rename"
			if (err == nil) != valid {
				t.Fatalf("unexpected resolution: %v", err)
			}
			after, _ := json.Marshal(inventory)
			if string(before) != string(after) {
				t.Fatal("resolver mutated inventory")
			}
			if !valid {
				return
			}
			if target.device.ID != "ap-id" || target.ethernetMAC != "02:00:00:00:00:01" {
				t.Fatal("wrong AP selected")
			}
			member, err := target.member(ref.Name, mode == "rename")
			if err != nil || member["macAddress"] != target.ethernetMAC || member["apName"] != nil {
				t.Fatal("unstable API selector", err)
			}
			if mode == "rename" {
				inventory[0].Hostname = ref.Name
				next, err := resolveAccessPointConfigurationTarget(ref, inventory)
				if err != nil {
					t.Fatal("rename broke resolution", err)
				}
				repeated, err := next.member(ref.Name, true)
				if err != nil || !reflect.DeepEqual(member, repeated) {
					t.Fatal("successful rename changed desired payload", err)
				}
			} else if len(member) != 1 {
				t.Fatal("unrequested rename")
			}
		})
	}
}
