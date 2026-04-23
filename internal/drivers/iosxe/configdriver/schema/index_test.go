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

// TestFamiliesMatchesPhase1Writers pins the families file to the set the
// writers package registers. Drift between the two is caught at unit-test
// time rather than at runtime when a writer asks for a missing family
// entry.
func TestFamiliesMatchesPhase1Writers(t *testing.T) {
	fam, err := LoadFamilies()
	if err != nil {
		t.Fatalf("LoadFamilies: %v", err)
	}

	want := []string{
		"access_list_extended",
		"dhcp",
		"interface_ethernet",
		"interface_loopback",
		"interface_virtual_port_group",
		"system",
		"vlan",
		"vrf",
	}

	got := make([]string, 0, len(fam))
	for name := range fam {
		got = append(got, name)
	}
	sort.Strings(got)

	if len(got) != len(want) {
		t.Fatalf("family count = %d, want %d\nnames = %v", len(got), len(want), got)
	}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("family[%d] = %q, want %q", i, got[i], name)
		}
	}

	// Every family must declare at least one YANG path; an empty path is
	// always a bug — a writer has nothing to read or write against.
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
