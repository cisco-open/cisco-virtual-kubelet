// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"reflect"
	"testing"
)

func ewlcScopeFixture() (map[string]fabricDeviceMembership, []Device, []fabricEWLCSetting) {
	members := map[string]fabricDeviceMembership{}
	devices := []Device{}
	for i, id := range []string{"a", "b", "c"} {
		members[id] = fabricDeviceMembership{ID: "member-" + id, NetworkDeviceID: id, FabricID: "fabric", Roles: []string{"EDGE_NODE"}}
		devices = append(devices, Device{ID: id, Serial: []string{"FOC12345678", "FOC23456789", "FOC34567890"}[i], Family: "Switches and Hubs", Reachability: "Reachable"})
	}
	return members, devices, []fabricEWLCSetting{{ID: "a", Enabled: true, Rolling: fabricEWLCRolling{Percentage: 25}}}
}

func TestFabricEWLCScope(t *testing.T) {
	members, devices, settings := ewlcScopeFixture()
	scope, err := resolveFabricEWLCScope("fabric", members, devices, settings)
	if err != nil || len(scope.physical) != 3 || len(scope.digest) != 64 {
		t.Fatal(scope, err)
	}
	want := fabricEWLCSettings{enabled: true, percentage: 25}
	_, current, err := scope.target("b", want)
	if err != nil || current != nil {
		t.Fatal(current, err)
	}
	_, current, err = scope.target("a", want)
	if err != nil || current == nil || !current.Enabled {
		t.Fatal(current, err)
	}
	current.Enabled = false
	if !scope.settings[0].Enabled {
		t.Fatal("target observation aliases scope")
	}
	reversed := []Device{devices[2], devices[1], devices[0]}
	again, err := resolveFabricEWLCScope("fabric", members, reversed, settings)
	if err != nil || again.digest != scope.digest {
		t.Fatal("inventory order changed digest", err)
	}
	settings[0].Rolling.Percentage = 15
	changed, err := resolveFabricEWLCScope("fabric", members, devices, settings)
	if err != nil || changed.digest == scope.digest || scope.settings[0].Rolling.Percentage != 25 {
		t.Fatal("settings not retained", err)
	}
	settings = append(settings, fabricEWLCSetting{ID: "b", Enabled: true, Rolling: fabricEWLCRolling{Percentage: 25}})
	full, err := resolveFabricEWLCScope("fabric", members, devices, settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := full.target("c", want); err == nil {
		t.Fatal("third controller allowed")
	}
	want.enabled = false
	if _, _, err := full.target("c", want); err == nil {
		t.Fatal("missing row treated as disabled")
	}
	if _, _, err := full.target("a", want); err != nil {
		t.Fatal("observed disable blocked", err)
	}
}

func TestFabricEWLCScopeRejectsUnboundEvidence(t *testing.T) {
	for _, mode := range []string{"missing-device", "duplicate-serial", "foreign-membership", "orphan-setting", "standalone-wlc", "unreachable", "wrong-family", "unknown-role", "duplicate-role", "duplicate-setting", "null-settings", "bad-percentage"} {
		t.Run(mode, func(t *testing.T) {
			members, devices, settings := ewlcScopeFixture()
			switch mode {
			case "missing-device":
				devices = devices[:2]
			case "duplicate-serial":
				devices[2].Serial = devices[0].Serial
			case "foreign-membership":
				m := members["a"]
				m.FabricID = "other"
				members["a"] = m
			case "orphan-setting":
				settings[0].ID = "absent"
			case "standalone-wlc":
				m := members["a"]
				m.Roles = []string{"WIRELESS_CONTROLLER_NODE"}
				members["a"] = m
			case "unreachable":
				devices[0].Reachability = "Unreachable"
			case "wrong-family":
				devices[0].Family = "Wireless Controller"
			case "unknown-role":
				m := members["c"]
				m.Roles = []string{"UNKNOWN"}
				members["c"] = m
			case "duplicate-role":
				m := members["c"]
				m.Roles = []string{"EDGE_NODE", "EDGE_NODE"}
				members["c"] = m
			case "duplicate-setting":
				settings = append(settings, settings[0])
			case "null-settings":
				settings = nil
			case "bad-percentage":
				settings[0].Rolling.Percentage = 10
			}
			if _, err := resolveFabricEWLCScope("fabric", members, devices, settings); err == nil {
				t.Fatal("accepted invalid scope")
			}
		})
	}
	members, devices, settings := ewlcScopeFixture()
	scope, err := resolveFabricEWLCScope("fabric", members, devices, settings)
	if err != nil {
		t.Fatal(err)
	}
	member := members["a"]
	member.Roles[0] = "BORDER_NODE"
	if !reflect.DeepEqual(scope.members[0].Roles, []string{"EDGE_NODE"}) {
		t.Fatal("membership evidence aliased input")
	}
}
