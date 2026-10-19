// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "testing"

func TestConfigurationDeviceIdentity(t *testing.T) {
	inventory := []Device{{ID: "one", Hostname: "switch.example", ManagementIP: "192.0.2.1", Serial: "SERIAL1", PlatformID: "C9300"}, {ID: "two", Hostname: "other.example", ManagementIP: "192.0.2.2", Serial: "SERIAL2", PlatformID: "C9300"}, {ID: "ap", Hostname: "AP-A", ManagementIP: "192.0.2.3", Serial: "APSERIAL", PlatformID: "C9120"}}
	for _, tc := range []struct {
		name string
		ref  configurationDeviceReference
		id   string
	}{
		{"fqdn", configurationDeviceReference{Name: "alias", FQDN: "switch.example"}, "one"},
		{"ip", configurationDeviceReference{Name: "alias", IP: "192.0.2.1"}, "one"},
		{"serial", configurationDeviceReference{Serial: "serial1"}, "one"},
		{"conflict", configurationDeviceReference{Name: "switch.example", IP: "192.0.2.2"}, ""},
		{"stale-ip", configurationDeviceReference{Name: "switch.example", IP: "192.0.2.9"}, ""},
		{"stale-serial", configurationDeviceReference{Name: "switch.example", Serial: "UNKNOWN"}, ""},
		{"ap-ip", configurationDeviceReference{IP: "192.0.2.3"}, ""},
		{"ap-name", configurationDeviceReference{Name: "AP-A"}, ""},
		{"ap-serial", configurationDeviceReference{Serial: "APSERIAL", IP: "192.0.2.99"}, "ap"},
		{"invalid-ip", configurationDeviceReference{IP: "switch.example"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveConfigurationDevice(tc.ref, inventory)
			if tc.id == "" {
				if err == nil {
					t.Fatal("unsafe selector accepted")
				}
			} else if err != nil || got.ID != tc.id {
				t.Fatal("wrong device", err)
			}
		})
	}
	duplicate := append(append([]Device{}, inventory...), inventory[0])
	duplicate[3].ID = "duplicate"
	if _, err := resolveConfigurationDevice(configurationDeviceReference{Name: "switch.example"}, duplicate); err == nil {
		t.Fatal("duplicate hostname accepted")
	}
}
