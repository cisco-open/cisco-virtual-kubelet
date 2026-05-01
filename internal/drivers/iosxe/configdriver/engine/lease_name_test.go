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

package engine

// Wave 8.1 regression tests for external-review-wave7-residuals
// Finding #1. The previous leaseName(device, family) produced
// names like "cvk-edge-01-interface_ethernet" — the underscore
// violates DNS-1123 subdomain rules and a real apiserver rejects
// every such Lease.create. fake.Client skipped name validation,
// so the bug only surfaced in a live cluster.
//
// These tests exercise the IsDNS1123Subdomain rule directly via
// k8s.io/apimachinery/pkg/util/validation, so a future regression
// in leaseName fails the unit suite even when fake.Client is the
// only k8s client in the test process.

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
)

// allShippedFamilies mirrors the union of phase1/2/3 family names
// from internal/drivers/iosxe/configdriver/writers/registry_test.go.
// Duplicating the list here keeps the engine package's tests
// independent of the writers package's internal test fixtures —
// the engine doesn't import the writers test data.
//
// If a new family is added to families.yaml that isn't in this
// list, this test won't catch a name-validation issue for it. The
// writers/registry test still pins family completeness; this test
// is the engine-side guard against name-validation regressions.
var allShippedFamilies = []string{
	// Phase 1
	"system", "vlan", "vrf", "interface_ethernet", "interface_loopback",
	"interface_virtual_port_group", "dhcp", "access_list_extended",
	// Phase 2 (representative subset — all underscore-bearing)
	"access_list_standard", "interface_switchport", "interface_port_channel",
	"interface_tunnel", "interface_vlan", "static_route", "route_map",
	"prefix_list", "ip_name_server", "ip_domain", "ip_ssh", "ip_http",
	"ip_nat_inside_source", "ip_nat_pool", "ip_as_path_access_list",
	"ip_community_list", "ipv6_access_list_extended",
	"ipv6_access_list_standard", "ipv6_prefix_list",
	// Phase 3 (representative subset)
	"crypto_ikev2_profile", "crypto_ipsec_profile",
	"crypto_ipsec_transform_set", "crypto_map", "crypto_pki_trustpoint",
	"radius_server", "tacacs_server", "snmp_server", "spanning_tree",
	"event_manager", "policy_map", "class_map", "ntp", "logging",
	"banner", "bgp", "ospf", "isis", "eigrp", "errdisable", "cdp",
	"lldp", "track", "username", "line", "clock",
}

func TestLeaseName_AllShippedFamiliesAreDNS1123Subdomain(t *testing.T) {
	t.Parallel()
	devices := []string{
		"edge-01",
		"core-router-1",
		"dc1-leaf-100",
	}
	for _, dev := range devices {
		for _, fam := range allShippedFamilies {
			name := leaseName(dev, fam)
			if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
				t.Errorf("leaseName(%q, %q) = %q is not a valid DNS-1123 subdomain: %v",
					dev, fam, name, errs)
			}
			// Sanity: the prefix is stable.
			if !strings.HasPrefix(name, "cvk-") {
				t.Errorf("leaseName(%q, %q) = %q missing 'cvk-' prefix", dev, fam, name)
			}
		}
	}
}

func TestLeaseName_HostileInputsAreSanitised(t *testing.T) {
	t.Parallel()
	cases := []struct {
		device, family string
	}{
		// Real-world IOS-XE family names with underscores.
		{"edge-01", "interface_ethernet"},
		{"edge-01", "access_list_extended"},
		{"edge-01", "ip_name_server"},
		// Hypothetical hostile inputs — defence in depth even though
		// CRD validation should never let them reach the controller.
		{"EDGE-01", "INTERFACE_ETHERNET"},
		{"edge.01", "interface/ethernet"},
		{"edge_01", "interface ethernet"},
		{"edge..01", "interface__ethernet"},
		// Trailing/leading separators.
		{"-edge-", "_interface_"},
	}
	for _, tc := range cases {
		name := leaseName(tc.device, tc.family)
		if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
			t.Errorf("leaseName(%q, %q) = %q invalid: %v", tc.device, tc.family, name, errs)
		}
	}
}

// TestLeaseName_DistinctInputsProduceDistinctNames pins the
// hash-suffix property: two distinct (device, family) pairs that
// happen to fold to the same sanitised middle still produce
// different lease names because the hash differs.
func TestLeaseName_DistinctInputsProduceDistinctNames(t *testing.T) {
	t.Parallel()
	a := leaseName("edge-01", "interface_ethernet")
	b := leaseName("edge-01", "interface-ethernet") // sanitises to the same middle
	if a == b {
		t.Errorf("two distinct family names folded to the same lease name:\n  a = %q\n  b = %q", a, b)
	}
}

// TestLeaseName_DeterministicAcrossInvocations pins idempotence —
// repeated calls with the same input produce the same name. This is
// the contract acquireLeases relies on (Get-then-renew vs create).
func TestLeaseName_DeterministicAcrossInvocations(t *testing.T) {
	t.Parallel()
	for i := 0; i < 5; i++ {
		a := leaseName("edge-01", "interface_ethernet")
		b := leaseName("edge-01", "interface_ethernet")
		if a != b {
			t.Errorf("non-deterministic leaseName: %q vs %q", a, b)
		}
	}
}

func TestSanitiseLeaseSegment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"interface_ethernet", "interface-ethernet"},
		{"INTERFACE_ETHERNET", "interface-ethernet"},
		{"edge.01", "edge-01"},
		{"edge..01", "edge-01"},
		{"_leading", "leading"},
		{"trailing_", "trailing"},
		{"", "x"},
		{"___", "x"},
		{"vlan", "vlan"},
		{"abc-def", "abc-def"},
	}
	for _, c := range cases {
		got := sanitiseLeaseSegment(c.in)
		if got != c.want {
			t.Errorf("sanitiseLeaseSegment(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
