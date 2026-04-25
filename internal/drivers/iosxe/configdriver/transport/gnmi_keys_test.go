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

package transport

// Wave 5A regression tests for external-review Finding #12: gNMI
// keyed-path conversion must use the YANG list-key field name from
// the schema, not guess `name` for strings and `id` for numbers.

import (
	"testing"
)

func TestParseGNMIPath_RegisteredKeyOverridesHeuristic(t *testing.T) {
	// Pre-fix this test would assert that `vlan-list=10` produced a
	// PathElem with key={"id": "10"} (numeric heuristic). Post-fix,
	// once the schema layer registers vlan-list → id, the result is
	// the same — but for a list whose YANG key is `tag` (route-target,
	// for example) the heuristic was wrong and the registry is right.
	// We exercise both: the heuristic-equivalent vlan-list case (no
	// regression) and a registered-key case (the actual fix).

	t.Run("heuristic preserved when no registration", func(t *testing.T) {
		// Use a unique segment name so no other registration interferes.
		path, err := parseGNMIPath("/some-unregistered-list=42")
		if err != nil {
			t.Fatalf("parseGNMIPath: %v", err)
		}
		if len(path.Elem) != 1 {
			t.Fatalf("expected 1 elem, got %d", len(path.Elem))
		}
		// Numeric value, no registration → fall back to "id".
		if k := path.Elem[0].Key; k["id"] != "42" {
			t.Errorf("heuristic fallback: expected key[id]=42, got %v", k)
		}
	})

	t.Run("registered key wins over heuristic", func(t *testing.T) {
		// Register a list whose YANG key is `tag`, which the value-type
		// heuristic could never guess.
		RegisterPathKey("route-target-export", "tag")
		path, err := parseGNMIPath("/route-target-export=65000:1")
		if err != nil {
			t.Fatalf("parseGNMIPath: %v", err)
		}
		if len(path.Elem) != 1 {
			t.Fatalf("expected 1 elem, got %d", len(path.Elem))
		}
		k := path.Elem[0].Key
		if k["tag"] != "65000:1" {
			t.Errorf("registered key: expected key[tag]=65000:1, got %v", k)
		}
		if _, ok := k["name"]; ok {
			t.Errorf("registered key: 'name' should not appear when 'tag' is registered, got %v", k)
		}
	})

	t.Run("registered key wins over numeric heuristic", func(t *testing.T) {
		RegisterPathKey("prefix-list-entry", "seq")
		path, err := parseGNMIPath("/prefix-list-entry=10")
		if err != nil {
			t.Fatalf("parseGNMIPath: %v", err)
		}
		k := path.Elem[0].Key
		if k["seq"] != "10" {
			t.Errorf("expected key[seq]=10 (registered), got %v", k)
		}
		if _, ok := k["id"]; ok {
			t.Errorf("'id' should not appear once 'seq' is registered, got %v", k)
		}
	})
}

func TestRegisterPathKey_Idempotent(t *testing.T) {
	// Re-registration with the same value is a no-op (no panic, last-
	// wins semantics for the first key field). The schema layer's
	// LoadFamilies is called once per binary today, but tests and
	// future multi-load callers must not panic on a double call.
	RegisterPathKey("idempotent-segment", "key1")
	RegisterPathKey("idempotent-segment", "key1")
	if got := pathKeyFor("idempotent-segment"); got != "key1" {
		t.Errorf("idempotent registration: got %q, want %q", got, "key1")
	}
}

func TestRegisterPathKey_EmptyInputsAreNoOp(t *testing.T) {
	// Defensive: families.yaml may have an entry with shape=keyed_list
	// but a missing key_fields list. The registration call must not
	// panic and must not pollute the registry with empty values.
	RegisterPathKey("", "name")     // empty segment
	RegisterPathKey("seg-without")  // no keys
	if got := pathKeyFor(""); got != "" {
		t.Errorf("empty segment should not be registered, got %q", got)
	}
	if got := pathKeyFor("seg-without"); got != "" {
		t.Errorf("zero-key registration should not be persisted, got %q", got)
	}
}

func TestLastPathSegment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"/", ""},
		{"/Cisco-IOS-XE-native:native", "native"},
		{"/Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list", "vlan-list"},
		{"native/vrf/definition", "definition"},
		{"/openconfig-system:system", "system"},
	}
	for _, tc := range cases {
		if got := LastPathSegment(tc.in); got != tc.want {
			t.Errorf("LastPathSegment(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
