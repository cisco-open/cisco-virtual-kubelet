// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package schema

import (
	"reflect"
	"slices"
	"testing"
)

func TestSupportedCoverageMatchesRegisteredFamilies(t *testing.T) {
	got := make([]string, 0, len(SupportedCoverage()))
	for _, entry := range SupportedCoverage() {
		got = append(got, entry.Family)
		if entry.Section == "" {
			t.Fatalf("%s has empty section", entry.Family)
		}
		if len(entry.SupportedFields) == 0 {
			t.Fatalf("%s has no supported fields", entry.Family)
		}
	}
	if !reflect.DeepEqual(got, Families) {
		t.Fatalf("supported coverage=%v, registered families=%v", got, Families)
	}
}

func TestCoverageMatrixIncludesProductionRoadmap(t *testing.T) {
	for _, family := range []string{
		"interface_switchport",
		"interface_vlan",
		"interface_loopback",
		"interface_port_channel",
		"vrf",
		"static_route",
		"ntp",
		"logging",
		"snmp_server",
	} {
		if !coverageHas(family, CoveragePlanned) {
			t.Fatalf("coverage matrix missing planned family %q", family)
		}
	}
	for _, family := range []string{"evpn", "nve"} {
		if !coverageHas(family, CoverageDeferred) {
			t.Fatalf("coverage matrix missing deferred family %q", family)
		}
	}
}

func TestCoverageAccessorsReturnCopies(t *testing.T) {
	supported := SupportedCoverage()
	if len(supported) == 0 {
		t.Fatal("expected supported coverage")
	}
	supported[0].SupportedFields[0] = "mutated"

	fresh := SupportedCoverage()
	if fresh[0].SupportedFields[0] == "mutated" {
		t.Fatal("SupportedCoverage returned shared field slice")
	}
}

func coverageHas(family string, state CoverageState) bool {
	return slices.ContainsFunc(CoverageMatrix, func(entry FamilyCoverage) bool {
		return entry.Family == family && entry.State == state
	})
}
