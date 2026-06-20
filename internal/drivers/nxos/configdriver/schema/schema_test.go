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

func TestCoverageMatrixIncludesNetAsCodeDataModelTarget(t *testing.T) {
	required := map[string]string{
		"aaa":                    "device",
		"analytics":              "device",
		"arp":                    "device",
		"banner":                 "device",
		"bfd":                    "device",
		"bgp":                    "device",
		"cdp":                    "device",
		"clock":                  "device",
		"community_list":         "device",
		"dhcp":                   "device",
		"dns":                    "device",
		"evpn":                   "device",
		"fabric_forwarding":      "device",
		"feature":                "device",
		"feature_set":            "device",
		"hsrp":                   "device",
		"hypershield":            "device",
		"ip_access_list":         "device",
		"ip_prefix_list":         "device",
		"ip_route":               "device",
		"ipv6_access_list":       "device",
		"ipv6_prefix_list":       "device",
		"ipv6_route":             "device",
		"isis":                   "device",
		"key_chain":              "device",
		"lldp":                   "device",
		"logging":                "device",
		"nd":                     "device",
		"netflow":                "device",
		"ntp":                    "device",
		"nxapi":                  "device",
		"ospf":                   "device",
		"ospfv3":                 "device",
		"pim":                    "device",
		"ptp":                    "device",
		"qos":                    "device",
		"route_map":              "device",
		"security_group":         "device",
		"sflow":                  "device",
		"snmp":                   "device",
		"span":                   "device",
		"spanning_tree":          "device",
		"ssh":                    "device",
		"system":                 "device",
		"telemetry":              "device",
		"udld":                   "device",
		"vlan":                   "device",
		"vpc":                    "device",
		"vrf":                    "device",
		"interface_ethernet":     "interface",
		"interface_loopback":     "interface",
		"interface_management":   "interface",
		"interface_nve":          "interface",
		"interface_port_channel": "interface",
		"interface_subinterface": "interface",
		"interface_vlan":         "interface",
	}

	for family, section := range required {
		entry, ok := coverageEntry(family)
		if !ok {
			t.Fatalf("coverage matrix missing NetAsCode family %q", family)
		}
		if entry.Section != section {
			t.Fatalf("%s section=%q, want %q", family, entry.Section, section)
		}
		if entry.State == "" {
			t.Fatalf("%s has empty state", family)
		}
	}
}

func TestCoverageMatrixFamiliesAreUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for _, entry := range CoverageMatrix {
		if entry.Family == "" {
			t.Fatal("coverage matrix contains empty family")
		}
		if _, ok := seen[entry.Family]; ok {
			t.Fatalf("coverage matrix contains duplicate family %q", entry.Family)
		}
		seen[entry.Family] = struct{}{}
	}
}

func TestSourcePatternMatrixIncludesNetAsCodeEntityPatterns(t *testing.T) {
	for _, pattern := range []string{
		"nxos.devices[].configuration",
		"nxos.device_groups[].configuration",
		"nxos.global.configuration",
		"nxos.variables",
		"nxos.templates[type=model]",
		"nxos.interface_groups",
		"interfaces.ethernets",
	} {
		if !sourcePatternHas(pattern, CoverageSupported) {
			t.Fatalf("source pattern matrix missing supported pattern %q", pattern)
		}
	}
	for _, pattern := range []string{
		"managed_devices",
		"managed_device_groups",
	} {
		if !sourcePatternHas(pattern, CoveragePlanned) {
			t.Fatalf("source pattern matrix missing planned pattern %q", pattern)
		}
	}
	for _, pattern := range []string{
		"yaml_files/yaml_directories",
		"template_files/template_directories",
		"write_model_file",
	} {
		if !sourcePatternHas(pattern, CoverageDeferred) {
			t.Fatalf("source pattern matrix missing deferred pattern %q", pattern)
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

func coverageEntry(family string) (FamilyCoverage, bool) {
	for _, entry := range CoverageMatrix {
		if entry.Family == family {
			return entry, true
		}
	}
	return FamilyCoverage{}, false
}

func sourcePatternHas(pattern string, state CoverageState) bool {
	return slices.ContainsFunc(SourcePatternMatrix, func(entry SourcePatternCoverage) bool {
		return entry.Pattern == pattern && entry.State == state
	})
}
