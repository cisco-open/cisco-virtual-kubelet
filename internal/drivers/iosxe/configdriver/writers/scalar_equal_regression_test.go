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

import "testing"

// TestScalarEqualHandlesUncomparableTypes is a regression test for the
// runtime panic discovered against a live Cat9300-24P running IOS-XE
// 17.18.2: when the device returned a banner motd as a YANG-RPC
// structure with a nested map value, scalarEqual's plain `==` panicked
// with "comparing uncomparable type map[string]interface {}". The fix
// classifies map / slice / func leaves as not-comparable and falls
// back to reflect.DeepEqual + stringified compare.
func TestScalarEqualHandlesUncomparableTypes(t *testing.T) {
	cases := []struct {
		name    string
		a, b    any
		want    bool
	}{
		{"both nil", nil, nil, true},
		{"nil vs string", nil, "x", false},
		{"map equal", map[string]any{"k": "v"}, map[string]any{"k": "v"}, true},
		{"map differ", map[string]any{"k": "v"}, map[string]any{"k": "w"}, false},
		{"slice equal", []any{1, 2}, []any{1, 2}, true},
		{"slice differ", []any{1, 2}, []any{1, 3}, false},
		{"map vs string", map[string]any{"k": "v"}, "v", false},
		{"int equal", 7, 7, true},
		{"string equal", "x", "x", true},
		{"yang true vs bool", "true", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scalarEqual(tc.a, tc.b); got != tc.want {
				t.Fatalf("scalarEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestLeavesEqualUncomparableLeavesNoPanic walks the path that paniced
// in production: a writer's managed leaf maps to a value of type
// map[string]any on the observed side. Pre-fix this called scalarEqual
// directly with a map, which panicked under `a == b`.
func TestLeavesEqualUncomparableLeavesNoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("leavesEqual panicked: %v", r)
		}
	}()
	desired := map[string]any{"banner": map[string]any{"text": "x"}}
	observed := map[string]any{"banner": map[string]any{"text": "y"}}
	if leavesEqual(desired, observed, []string{"banner"}) {
		t.Fatalf("expected leaves to differ")
	}
}
