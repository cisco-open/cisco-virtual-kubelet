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

package intent

import (
	"reflect"
	"testing"
)

func m(pairs ...any) map[string]any {
	out := map[string]any{}
	for i := 0; i < len(pairs); i += 2 {
		out[pairs[i].(string)] = pairs[i+1]
	}
	return out
}

func TestMergeScalarRightmostWins(t *testing.T) {
	t.Parallel()
	got := Merge("left", "right")
	if got != "right" {
		t.Fatalf("got %v, want right", got)
	}
}

func TestMergeNilDefers(t *testing.T) {
	t.Parallel()
	if got := Merge(nil, "x"); got != "x" {
		t.Fatalf("nil dst: got %v, want x", got)
	}
	if got := Merge("x", nil); got != "x" {
		t.Fatalf("nil src: got %v, want x", got)
	}
}

func TestMergeObjectRecurses(t *testing.T) {
	t.Parallel()
	dst := m("a", 1, "b", m("x", 10, "y", 20))
	src := m("b", m("y", 99, "z", 30), "c", 3)
	got := Merge(dst, src).(map[string]any)

	want := m(
		"a", 1,
		"b", m("x", 10, "y", 99, "z", 30),
		"c", 3,
	)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

func TestMergeKeyedListByName(t *testing.T) {
	t.Parallel()
	dst := []any{
		m("name", "A", "val", 1),
		m("name", "B", "val", 2),
	}
	src := []any{
		m("name", "B", "val", 22, "extra", true),
		m("name", "C", "val", 3),
	}
	got := Merge(dst, src).([]any)

	want := []any{
		m("name", "A", "val", 1),
		m("name", "B", "val", 22, "extra", true),
		m("name", "C", "val", 3),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

// When elements carry only id (no name), the fallback heuristic keys by id.
func TestMergeKeyedListByIdFallback(t *testing.T) {
	t.Parallel()
	dst := []any{m("id", 10, "val", 1), m("id", 20, "val", 2)}
	src := []any{m("id", 20, "val", 22)}
	got := Merge(dst, src).([]any)
	want := []any{m("id", 10, "val", 1), m("id", 20, "val", 22)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

// With a path-rule that pins the key field to id, merge is id-keyed even
// when name is also present — this is the path the IntentResolver takes
// for YANG-keyed lists like vlan.vlans (key=id).
func TestMergeWithRulesOverridesHeuristic(t *testing.T) {
	t.Parallel()
	dst := []any{m("id", 10, "name", "users"), m("id", 20, "name", "voice")}
	src := []any{m("id", 20, "name", "voice-v2")}
	rules := KeyRules{"vlan.vlans": "id"}
	got := MergeWithRules(
		m("vlan", m("vlans", dst)),
		m("vlan", m("vlans", src)),
		rules,
	).(map[string]any)

	vlans := got["vlan"].(map[string]any)["vlans"].([]any)
	want := []any{m("id", 10, "name", "users"), m("id", 20, "name", "voice-v2")}
	if !reflect.DeepEqual(vlans, want) {
		t.Fatalf("got %#v\nwant %#v", vlans, want)
	}
}

func TestMergeScalarListReplaces(t *testing.T) {
	t.Parallel()
	dst := []any{"a", "b", "c"}
	src := []any{"x", "y"}
	got := Merge(dst, src).([]any)
	if !reflect.DeepEqual(got, src) {
		t.Fatalf("got %v, want %v", got, src)
	}
}

// Mixed (objects-without-shared-key) lists fall back to scalar-list
// replacement because netascode has no defined union semantics for them.
func TestMergeMixedObjectListReplaces(t *testing.T) {
	t.Parallel()
	dst := []any{m("foo", 1), m("bar", 2)}
	src := []any{m("baz", 3)}
	got := Merge(dst, src).([]any)
	if !reflect.DeepEqual(got, src) {
		t.Fatalf("got %v, want %v", got, src)
	}
}

func TestMergeDoesNotMutateInputs(t *testing.T) {
	t.Parallel()
	dst := m("a", m("x", 1))
	src := m("a", m("x", 2, "y", 3))

	dstBefore := m("a", m("x", 1))
	srcBefore := m("a", m("x", 2, "y", 3))

	_ = Merge(dst, src)

	if !reflect.DeepEqual(dst, dstBefore) {
		t.Errorf("Merge mutated dst: %v", dst)
	}
	if !reflect.DeepEqual(src, srcBefore) {
		t.Errorf("Merge mutated src: %v", src)
	}
}

func TestMergeTypeMismatchRightmostWins(t *testing.T) {
	t.Parallel()
	// Left is a map, right is a scalar — netascode says rightmost wins
	// rather than erroring; the semantics are "operator typed it, trust them".
	got := Merge(m("k", 1), "replaced")
	if got != "replaced" {
		t.Fatalf("got %v, want replaced", got)
	}
}

func TestEqual(t *testing.T) {
	t.Parallel()
	a := m("x", m("y", []any{1, 2, 3}))
	b := m("x", m("y", []any{1, 2, 3}))
	c := m("x", m("y", []any{1, 2, 4}))
	if !Equal(a, b) {
		t.Error("Equal(a,b) = false, want true")
	}
	if Equal(a, c) {
		t.Error("Equal(a,c) = true, want false")
	}
}

// End-to-end merge through the four netascode scopes: defaults, device
// group, template-expansion, per-device. Rightmost wins. Uses explicit
// rules for YANG-keyed lists (vlan.vlans keyed by id) as the resolver
// will in production.
func TestNetascodeScopePrecedence(t *testing.T) {
	t.Parallel()
	rules := KeyRules{"vlan.vlans": "id"}

	defaults := m("system", m("login_on_failure", true, "mtu", 1500))
	group := m("system", m("mtu", 9000), "vlan", m("vlans", []any{m("id", 10, "name", "users")}))
	template := m("vlan", m("vlans", []any{m("id", 20, "name", "voice")}))
	perDevice := m("system", m("hostname", "edge-01"), "vlan", m("vlans", []any{m("id", 10, "name", "USERS")}))

	merge3 := func(a, b any) any { return MergeWithRules(a, b, rules) }
	merged := merge3(merge3(merge3(defaults, group), template), perDevice)

	want := m(
		"system", m("login_on_failure", true, "mtu", 9000, "hostname", "edge-01"),
		"vlan", m("vlans", []any{
			m("id", 10, "name", "USERS"),
			m("id", 20, "name", "voice"),
		}),
	)
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged = %#v\nwant %#v", merged, want)
	}
}
