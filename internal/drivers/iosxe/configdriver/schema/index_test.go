// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package schema

import (
	"sort"
	"testing"
)

// phase1Families is the set that must always remain present in the
// family index. The Phase-2 set is checked separately below so removals
// of either set fail distinctly.
var phase1Families = []string{
	"access_list_extended",
	"dhcp",
	"interface_ethernet",
	"interface_loopback",
	"interface_virtual_port_group",
	"system",
	"vlan",
	"vrf",
}

// phase2Families are the additional entries introduced with the Phase-2
// family set. Each entry's writer is a skeleton until its own PR adds a
// real implementation via writers.Override(). Presence here is the
// public contract that the family is in-scope and its YANG mapping has
// been reviewed.
var phase2Families = []string{
	"aaa",
	"access_list_standard",
	"banner",
	"bgp",
	"cdp",
	"interface_switchport",
	"line",
	"lldp",
	"logging",
	"ntp",
	"ospf",
	"prefix_list",
	"route_map",
	"snmp_server",
	"static_route",
}

// phase3Families complete the netascode IOS-XE family set. See
// families.yaml for the full list; this slice is the test-time pin.
var phase3Families = []string{
	"clock",
	"class_map",
	"crypto_ikev2_profile",
	"crypto_ipsec_profile",
	"crypto_ipsec_transform_set",
	"crypto_map",
	"crypto_pki_trustpoint",
	"eigrp",
	"errdisable",
	"event_manager",
	"interface_port_channel",
	"interface_tunnel",
	"interface_vlan",
	"ip_as_path_access_list",
	"ip_community_list",
	"ip_domain",
	"ip_http",
	"ip_name_server",
	"ip_nat_inside_source",
	"ip_nat_pool",
	"ip_ssh",
	"ipv6_access_list_extended",
	"ipv6_access_list_standard",
	"ipv6_prefix_list",
	"isis",
	"policy_map",
	"radius_server",
	"spanning_tree",
	"tacacs_server",
	"track",
	"username",
}

// TestFamiliesIndexConsistent pins both phase sets and checks every
// entry declares the leaves the engine expects. Drift between the
// index and the writers package is caught at unit-test time rather
// than at runtime when a writer asks for a missing entry.
func TestFamiliesIndexConsistent(t *testing.T) {
	fam, err := LoadFamilies()
	if err != nil {
		t.Fatalf("LoadFamilies: %v", err)
	}

	for _, name := range phase1Families {
		if _, ok := fam[name]; !ok {
			t.Errorf("Phase-1 family missing from index: %q", name)
		}
	}
	for _, name := range phase2Families {
		if _, ok := fam[name]; !ok {
			t.Errorf("Phase-2 family missing from index: %q", name)
		}
	}
	for _, name := range phase3Families {
		if _, ok := fam[name]; !ok {
			t.Errorf("Phase-3 family missing from index: %q", name)
		}
	}

	// Total must match the union — extra unknown entries are a review
	// signal (someone added a family without updating the test).
	expected := len(phase1Families) + len(phase2Families) + len(phase3Families)
	if len(fam) != expected {
		got := make([]string, 0, len(fam))
		for k := range fam {
			got = append(got, k)
		}
		sort.Strings(got)
		t.Errorf("family count=%d, want %d\ngot=%v", len(fam), expected, got)
	}

	for name, f := range fam {
		if len(f.YANGPaths) == 0 {
			t.Errorf("family %q has no yang_paths", name)
		}
		if f.Shape != "singleton" && f.Shape != "keyed_list" {
			t.Errorf("family %q has invalid shape %q", name, f.Shape)
		}
		if f.Shape == "keyed_list" && len(f.KeyFields) == 0 {
			t.Errorf("family %q has shape=keyed_list but no key_fields", name)
		}
	}
}

func TestDefaultYANGReleaseResolves(t *testing.T) {
	r, err := DefaultYANGRelease()
	if err != nil {
		t.Fatalf("DefaultYANGRelease: %v", err)
	}
	if r.Version == "" {
		t.Fatal("default YANG release has empty version")
	}
	if r.Status != "supported" {
		t.Fatalf("default YANG release status = %q, want supported", r.Status)
	}
}
