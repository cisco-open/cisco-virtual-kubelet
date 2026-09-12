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

package topology

import (
	"strings"
	"testing"
)

func TestCanonicalPhysicalIdentity(t *testing.T) {
	for _, test := range []struct {
		value string
		want  string
	}{
		{value: "FOC2416U0MV", want: "foc2416u0mv"},
		{value: "550E8400-E29B-41D4-A716-446655440000", want: "550e8400-e29b-41d4-a716-446655440000"},
		{value: "chassis/0:slot_1", want: "chassis/0:slot_1"},
	} {
		got, err := CanonicalPhysicalIdentity(test.value)
		if err != nil || got != test.want {
			t.Fatalf("CanonicalPhysicalIdentity(%q) = %q, %v; want %q", test.value, got, err, test.want)
		}
	}
	for _, invalid := range []string{"", " serial", "serial ", "-serial", "serial-", "serial value", "serial\nvalue", strings.Repeat("a", MaxPhysicalIdentityLength+1)} {
		if _, err := CanonicalPhysicalIdentity(invalid); err == nil {
			t.Fatalf("CanonicalPhysicalIdentity(%q) succeeded", invalid)
		}
	}
}

func TestObservedPhysicalIdentityUsesDeclarationAsAuthority(t *testing.T) {
	for _, test := range []struct {
		name, declared, machine, system, wantError string
	}{
		{name: "case normalized match", declared: "FOC2416U0MV", machine: "foc2416u0mv", system: "FOC2416U0MV"},
		{name: "one live field", declared: "serial-a", system: "SERIAL-A"},
		{name: "missing declaration", machine: "serial-a", system: "serial-a", wantError: "required"},
		{name: "missing observation", declared: "serial-a", wantError: "no live"},
		{name: "conflicting observations", declared: "serial-a", machine: "serial-a", system: "serial-b", wantError: "conflict"},
		{name: "observation mismatch", declared: "serial-a", machine: "serial-b", system: "serial-b", wantError: "does not match"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ObservedPhysicalIdentity(test.declared, test.machine, test.system)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("ObservedPhysicalIdentity() = %q, %v; want error %q", got, err, test.wantError)
				}
				return
			}
			if err != nil || got != strings.ToLower(test.declared) {
				t.Fatalf("ObservedPhysicalIdentity() = %q, %v", got, err)
			}
		})
	}
}
