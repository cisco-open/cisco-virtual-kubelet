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

package writers

import (
	"context"
	"errors"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver"
)

// phase1Families enumerates the set originally promised for Phase 1.
// Every entry here must remain registered; the Phase-2 set is checked
// separately so removing either set fails distinctly.
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

// phase2Families are the additional entries introduced with the
// Phase-2 family set. Writers may start out as skeletons and be
// replaced via Override as real implementations land; what this test
// pins is that the registration itself is present.
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

// phase3Families complete the netascode IOS-XE family set: management
// plane (users, DNS, SSH, HTTP, AAA servers), security (IPv6 ACLs,
// community/as-path lists, crypto), additional interfaces (VLAN SVI,
// Port-channel, Tunnel), additional routing (EIGRP, IS-IS), QoS
// (class-map, policy-map), NAT, tracking/EEM, and L2 globals.
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

func TestPhase1FamiliesRegistered(t *testing.T) {
	got := Families()
	for _, fam := range phase1Families {
		if !contains(got, fam) {
			t.Errorf("Phase-1 family %q not registered", fam)
		}
	}
	for _, fam := range phase2Families {
		if !contains(got, fam) {
			t.Errorf("Phase-2 family %q not registered", fam)
		}
	}
	for _, fam := range phase3Families {
		if !contains(got, fam) {
			t.Errorf("Phase-3 family %q not registered", fam)
		}
	}
	want := len(phase1Families) + len(phase2Families) + len(phase3Families)
	if Len() != want {
		t.Errorf("Len()=%d, want %d", Len(), want)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func TestGetReturnsRegisteredWriter(t *testing.T) {
	for _, fam := range phase1Families {
		t.Run(fam, func(t *testing.T) {
			w := Get(fam)
			if w == nil {
				t.Fatalf("Get(%q) returned nil", fam)
			}
			if w.Family() != fam {
				t.Fatalf("Family() = %q, want %q", w.Family(), fam)
			}
			if len(w.YANGPaths()) == 0 {
				t.Fatalf("YANGPaths() = empty for %q", fam)
			}
		})
	}
}

func TestGetReturnsNilForUnknown(t *testing.T) {
	if w := Get("not-a-real-family"); w != nil {
		t.Fatalf("Get(unknown) = %v, want nil", w)
	}
}

// TestSkeletonWritePathReturnsSentinel pins the contract that every
// skeleton error is errors.Is-matchable against configdriver.ErrNotImplemented
// so provider status code can distinguish scaffold from live device failures.
// Every Phase-1 family now ships a real writer; this test pulls a
// Phase-2 family that is still a skeleton and exercises its skeleton
// stub. Switch to a family that remains unimplemented as each family's
// real writer lands.
func TestSkeletonWritePathReturnsSentinel(t *testing.T) {
	// Register a skeleton explicitly so the test is stable against
	// Phase-2 writers landing in any order and replacing entries.
	skelName := "_test_skeleton_family_"
	registerSkeleton(skelName, "/Cisco-IOS-XE-native:native/test-only")
	t.Cleanup(func() {
		mu.Lock()
		delete(registry, skelName)
		mu.Unlock()
	})
	w := Get(skelName)
	if w == nil {
		t.Fatal("skeleton writer unexpectedly unregistered")
	}
	if _, err := w.Fetch(context.Background(), nil); !errors.Is(err, configdriver.ErrNotImplemented) {
		t.Fatalf("Fetch: got %v, want configdriver.ErrNotImplemented", err)
	}
	if _, err := w.Diff(nil, nil); !errors.Is(err, configdriver.ErrNotImplemented) {
		t.Fatalf("Diff: got %v, want configdriver.ErrNotImplemented", err)
	}
	if err := w.Apply(context.Background(), nil, nil); !errors.Is(err, configdriver.ErrNotImplemented) {
		t.Fatalf("Apply: got %v, want configdriver.ErrNotImplemented", err)
	}
}

func TestRegisterNilPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Register(nil): expected panic")
		}
	}()
	Register(nil)
}
