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

package provider

// Wave 2C regression tests for external-review Finding #7: the
// Conflict status condition must aggregate overlap across every
// family in spec.managedFamilies, not just the first.

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

func TestBuildConflictMessage_NoOverlap(t *testing.T) {
	t.Parallel()
	cr := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "cr-a", Namespace: "n"},
		Spec: configv1alpha1.IOSXEConfigSpec{
			ManagedFamilies: []string{"system", "vlan"},
		},
	}
	got := buildConflictMessage(cr, map[string][]string{})
	if got != "" {
		t.Errorf("expected empty message for no overlap, got %q", got)
	}
}

func TestBuildConflictMessage_OverlapOnFirstFamily(t *testing.T) {
	t.Parallel()
	cr := &configv1alpha1.IOSXEConfig{
		Spec: configv1alpha1.IOSXEConfigSpec{ManagedFamilies: []string{"system", "vlan"}},
	}
	got := buildConflictMessage(cr, map[string][]string{
		"system": {"n/other-cr"},
	})
	if !strings.Contains(got, "n/other-cr") || !strings.Contains(got, "system") {
		t.Errorf("expected message to name owner + family, got %q", got)
	}
}

// The headline test for Finding #7: overlap on the SECOND family was
// previously invisible because familiesKey only returned the first.
func TestBuildConflictMessage_OverlapOnSecondFamily(t *testing.T) {
	t.Parallel()
	cr := &configv1alpha1.IOSXEConfig{
		Spec: configv1alpha1.IOSXEConfigSpec{ManagedFamilies: []string{"system", "vlan"}},
	}
	got := buildConflictMessage(cr, map[string][]string{
		"vlan": {"n/other-cr"},
	})
	if got == "" {
		t.Fatalf("expected non-empty message; pre-fix this returned empty (bug)")
	}
	if !strings.Contains(got, "n/other-cr") || !strings.Contains(got, "vlan") {
		t.Errorf("message must name owner + the overlapping family, got %q", got)
	}
}

func TestBuildConflictMessage_AggregatesAcrossFamilies(t *testing.T) {
	t.Parallel()
	cr := &configv1alpha1.IOSXEConfig{
		Spec: configv1alpha1.IOSXEConfigSpec{ManagedFamilies: []string{"system", "vlan", "vrf"}},
	}
	// Two different owners, three overlap families.
	got := buildConflictMessage(cr, map[string][]string{
		"system": {"n/a"},
		"vlan":   {"n/b"},
		"vrf":    {"n/a"},
	})
	for _, want := range []string{"n/a", "n/b", "system", "vlan", "vrf"} {
		if !strings.Contains(got, want) {
			t.Errorf("message missing %q in %q", want, got)
		}
	}
	// Owner "n/a" overlaps on system AND vrf — both should appear in
	// its bracketed list, deduplicated against each other.
	idxA := strings.Index(got, "n/a on [")
	if idxA < 0 {
		t.Fatalf("expected 'n/a on [' in %q", got)
	}
	bracket := got[idxA+len("n/a on ["):]
	end := strings.Index(bracket, "]")
	if end < 0 {
		t.Fatalf("malformed message: %q", got)
	}
	families := strings.Split(bracket[:end], ",")
	if len(families) != 2 {
		t.Errorf("expected 2 families bracketed for n/a, got %v in %q", families, got)
	}
}

func TestBuildConflictMessage_Deterministic(t *testing.T) {
	t.Parallel()
	// Same conflict input produces the same message twice — sorted
	// owners and sorted families inside each bracket. Without
	// determinism, every reconcile tick churns the status condition
	// even when nothing changed.
	cr := &configv1alpha1.IOSXEConfig{
		Spec: configv1alpha1.IOSXEConfigSpec{ManagedFamilies: []string{"a", "b", "c"}},
	}
	conflicts := map[string][]string{
		"a": {"z/cr-2", "y/cr-1"},
		"c": {"y/cr-1"},
	}
	a := buildConflictMessage(cr, conflicts)
	b := buildConflictMessage(cr, conflicts)
	if a != b {
		t.Errorf("non-deterministic message:\n  a=%q\n  b=%q", a, b)
	}
}
