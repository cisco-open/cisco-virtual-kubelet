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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

func mkIntent(cfg map[string]any, families ...string) *ResolvedIntent {
	return &ResolvedIntent{
		DeviceName:      "edge-01",
		ManagedFamilies: families,
		Configuration:   cfg,
		DriftPolicy:     configv1alpha1.DriftPolicyRevert,
		SourceCR: &configv1alpha1.IOSXEConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "edge-01", Namespace: "network"},
		},
	}
}

func TestCanonicalHashStable(t *testing.T) {
	t.Parallel()
	a := mkIntent(m("vlan", m("vlans", []any{m("id", 10, "name", "users")})), "vlan")
	b := mkIntent(m("vlan", m("vlans", []any{m("id", 10, "name", "users")})), "vlan")

	ha, err := CanonicalHash(a)
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	hb, err := CanonicalHash(b)
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if ha != hb {
		t.Errorf("equivalent intents produced different hashes:\n  a=%s\n  b=%s", ha, hb)
	}
	if !strings.HasPrefix(ha, "sha256:") {
		t.Errorf("hash prefix missing: %s", ha)
	}
}

// A semantic change to the configuration must flip the hash even when
// only a nested leaf differs.
func TestCanonicalHashChangesWithConfiguration(t *testing.T) {
	t.Parallel()
	a := mkIntent(m("vlan", m("vlans", []any{m("id", 10, "name", "users")})), "vlan")
	b := mkIntent(m("vlan", m("vlans", []any{m("id", 10, "name", "USERS")})), "vlan")

	ha, _ := CanonicalHash(a)
	hb, _ := CanonicalHash(b)
	if ha == hb {
		t.Errorf("hash did not change for semantic difference: %s", ha)
	}
}

// Re-reading the same CR with a bumped resourceVersion/generation must
// NOT invalidate the hash — the reconciler short-circuit depends on this
// property.
func TestCanonicalHashIgnoresSourceCRMetadata(t *testing.T) {
	t.Parallel()
	a := mkIntent(m("system", m("hostname", "edge-01")), "system")
	b := mkIntent(m("system", m("hostname", "edge-01")), "system")
	a.SourceCR.Generation = 5
	a.SourceCR.ResourceVersion = "42"
	b.SourceCR.Generation = 6
	b.SourceCR.ResourceVersion = "99"

	ha, _ := CanonicalHash(a)
	hb, _ := CanonicalHash(b)
	if ha != hb {
		t.Errorf("hash changed with only metadata diff:\n  a=%s\n  b=%s", ha, hb)
	}
}

// Reordering map keys at authoring time (YAML allows any order) must not
// change the hash — that is the entire point of "canonical".
func TestCanonicalHashMapOrderIndependent(t *testing.T) {
	t.Parallel()
	a := mkIntent(m("a", 1, "b", 2, "c", 3), "x")
	b := mkIntent(m("c", 3, "a", 1, "b", 2), "x")

	ha, _ := CanonicalHash(a)
	hb, _ := CanonicalHash(b)
	if ha != hb {
		t.Errorf("hash changed with map-key order:\n  a=%s\n  b=%s", ha, hb)
	}
}

// List order, by contrast, IS semantically significant in netascode
// (order influences things like ACL sequence numbers), so it must affect
// the hash.
func TestCanonicalHashListOrderSignificant(t *testing.T) {
	t.Parallel()
	a := mkIntent(m("xs", []any{1, 2, 3}), "x")
	b := mkIntent(m("xs", []any{3, 2, 1}), "x")

	ha, _ := CanonicalHash(a)
	hb, _ := CanonicalHash(b)
	if ha == hb {
		t.Errorf("hash did not change with list order: %s", ha)
	}
}
