// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestFabricEWLCFoundationBinding(t *testing.T) {
	for _, mode := range []string{"valid", "missing-member", "changed-membership", "changed-role", "changed-peer", "missing-physical", "changed-target", "foreign-fabric", "missing-digest", "parent-footprint"} {
		t.Run(mode, func(t *testing.T) {
			members, devices, settings := ewlcScopeFixture()
			scope, err := resolveFabricEWLCScope("fabric", members, devices, settings)
			if err != nil {
				t.Fatal(err)
			}
			foundation := &fabricDeviceDependencies{target: scope.physical[0], physical: append([]Device(nil), scope.physical...), native: fabricDeviceConfiguration{NetworkDeviceID: "a", FabricID: "fabric", Roles: []string{"EDGE_NODE"}}, before: map[string]fabricDeviceConfiguration{}, digest: "foundation"}
			for _, m := range scope.members {
				foundation.before[m.NetworkDeviceID] = fabricDeviceConfiguration{ID: m.ID, NetworkDeviceID: m.NetworkDeviceID, FabricID: m.FabricID, Roles: append([]string(nil), m.Roles...)}
			}
			switch mode {
			case "missing-member":
				delete(foundation.before, "a")
			case "changed-membership":
				m := foundation.before["b"]
				m.ID = "replacement"
				foundation.before["b"] = m
			case "changed-role":
				foundation.native.Roles = []string{"BORDER_NODE"}
			case "changed-peer":
				foundation.physical[1].Serial = "FOC99999999"
			case "missing-physical":
				foundation.physical = foundation.physical[:2]
			case "changed-target":
				foundation.target.ManagementIP = "192.0.2.7"
			case "foreign-fabric":
				foundation.native.FabricID = "other"
			case "missing-digest":
				foundation.digest = ""
			case "parent-footprint":
				foundation.physical = append(foundation.physical, Device{ID: "parent", Serial: "foc45678901"})
			}
			want := fabricEWLCSettings{enabled: true, percentage: 25}
			got, err := bindFabricEWLCFoundation(foundation, scope, want)
			if mode != "valid" && mode != "parent-footprint" {
				if err == nil || got != nil {
					t.Fatal("accepted inconsistent foundation")
				}
				return
			}
			if err != nil || got.observed == nil || got.target.ID != "a" || len(got.digest) != 64 {
				t.Fatal(got, err)
			}
			if mode == "parent-footprint" && len(got.foundation.physical) != 4 {
				t.Fatal("lost parent scope")
			}
			want.percentage = 15
			changed, err := bindFabricEWLCFoundation(foundation, scope, want)
			if err != nil || changed.digest == got.digest {
				t.Fatal("intent change lost", err)
			}
		})
	}
}
