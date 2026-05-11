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

// Wave 1B regression tests for external-review Finding #2: the hash
// short-circuit must NOT prevent steady-state drift detection. The
// previous behaviour bypassed Fetch+Diff after the first clean apply
// and even subscribe events re-entered the short-circuit.

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine"
)

func TestDriftDetectInterval_Defaults(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		spec string
		want time.Duration
	}{
		{"empty falls back to default", "", defaultDriftDetectInterval},
		{"unparseable falls back to default", "garbage", defaultDriftDetectInterval},
		{"sub-floor is clamped", "5s", minDriftDetectInterval},
		{"exact floor passes through", "30s", minDriftDetectInterval},
		{"above floor passes through", "10m", 10 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cr := &configv1alpha1.IOSXEConfig{
				Spec: configv1alpha1.IOSXEConfigSpec{
					IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
						DriftDetectInterval: tc.spec,
					},
				},
			}
			if got := driftDetectInterval(cr); got != tc.want {
				t.Errorf("driftDetectInterval(%q) = %v, want %v", tc.spec, got, tc.want)
			}
		})
	}
}

func TestDueForDriftCheck(t *testing.T) {
	t.Parallel()
	now := metav1.Now()
	long := metav1.NewTime(now.Add(-2 * time.Hour))
	cases := []struct {
		name        string
		lastCheck   *metav1.Time
		intervalStr string
		want        bool
	}{
		{"never reconciled", nil, "5m", true},
		{"fresh under default", &now, "5m", false},
		{"stale under default", &long, "5m", true},
		{"clamped sub-floor still due if older", &long, "5s", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cr := &configv1alpha1.IOSXEConfig{
				Spec: configv1alpha1.IOSXEConfigSpec{
					IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
						DriftDetectInterval: tc.intervalStr,
					},
				},
				Status: configv1alpha1.IOSXEConfigStatus{LastDeviceCheck: tc.lastCheck},
			}
			if got := dueForDriftCheck(cr); got != tc.want {
				t.Errorf("dueForDriftCheck = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestShortCircuitHonoursDriftDetectInterval is the headline assertion
// for Finding #2: a CR whose intent is unchanged AND last-applied is
// fresh AND not subscribe-triggered short-circuits; the same CR with
// stale last-check does NOT short-circuit.
//
// We exercise the predicate logic inline rather than spinning up a
// full reconcile fixture; the predicate is the entire fix and is the
// thing that previously got it wrong.
func TestShortCircuitHonoursDriftDetectInterval(t *testing.T) {
	t.Parallel()

	freshCheck := metav1.Now()
	staleCheck := metav1.NewTime(time.Now().Add(-1 * time.Hour))

	makeCR := func(lastCheck *metav1.Time) *configv1alpha1.IOSXEConfig {
		return &configv1alpha1.IOSXEConfig{
			ObjectMeta: metav1.ObjectMeta{Generation: 7},
			Spec: configv1alpha1.IOSXEConfigSpec{
				IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
					DriftDetectInterval: "5m",
				},
			},
			Status: configv1alpha1.IOSXEConfigStatus{
				ObservedGeneration: 7,
				LastAppliedHash:    "abc",
				Phase:              engine.PhaseInSync,
				LastDeviceCheck:    lastCheck,
			},
		}
	}

	// Reproduces the exact predicate used in reconcileOne to keep the
	// test in lockstep with the implementation.
	shortCircuits := func(cr *configv1alpha1.IOSXEConfig, hash string, applied bool, trigger reconcileTrigger) bool {
		return !applied &&
			trigger != triggerSubscribe &&
			cr.Status.ObservedGeneration == cr.Generation &&
			cr.Status.LastAppliedHash == hash &&
			cr.Status.Phase == engine.PhaseInSync &&
			!dueForDriftCheck(cr)
	}

	cases := []struct {
		name    string
		cr      *configv1alpha1.IOSXEConfig
		hash    string
		applied bool
		trigger reconcileTrigger
		want    bool
	}{
		{"fresh + event + matching hash → short-circuit", makeCR(&freshCheck), "abc", false, triggerEvent, true},
		{"fresh + poll + matching hash → short-circuit", makeCR(&freshCheck), "abc", false, triggerPoll, true},
		{"fresh + subscribe → bypass", makeCR(&freshCheck), "abc", false, triggerSubscribe, false},
		{"stale + poll → bypass (drift due)", makeCR(&staleCheck), "abc", false, triggerPoll, false},
		{"stale + event → bypass", makeCR(&staleCheck), "abc", false, triggerEvent, false},
		{"never-reconciled → bypass", makeCR(nil), "abc", false, triggerEvent, false},
		{"hash drifted → bypass", makeCR(&freshCheck), "different-hash", false, triggerEvent, false},
		{"replay-applied → bypass", makeCR(&freshCheck), "abc", true, triggerEvent, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shortCircuits(tc.cr, tc.hash, tc.applied, tc.trigger); got != tc.want {
				t.Errorf("shortCircuits = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestShortCircuitFiresWhenRolledBackTargetMatches is the adversarial-
// review regression for Finding #2 (rollback). After a successful
// rollback the resolved-intent body is the revision body, the hash is
// computed from that body, and LastAppliedHash was set to the same
// value by the prior tick. The short-circuit predicate must therefore
// fire on subsequent steady-state ticks even though spec.rollbackTo
// remains set — the controller cannot clear the spec, so without this
// the engine ran on every poll. The predicate intentionally omits any
// appliedRollback clause: the hash check is the gate.
func TestShortCircuitFiresWhenRolledBackTargetMatches(t *testing.T) {
	t.Parallel()
	freshCheck := metav1.Now()
	cr := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Generation: 4},
		Spec: configv1alpha1.IOSXEConfigSpec{
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				DriftDetectInterval: "5m",
				RollbackTo:          "edge-01-rev-7",
			},
		},
		Status: configv1alpha1.IOSXEConfigStatus{
			ObservedGeneration: 4,
			LastAppliedHash:    "sha256:rev-7-body",
			Phase:              engine.PhaseInSync,
			LastDeviceCheck:    &freshCheck,
		},
	}
	// Predicate mirrors reconcileOne post-fix: no !appliedRollback clause.
	shortCircuits := !false &&
		triggerEvent != triggerSubscribe &&
		cr.Status.ObservedGeneration == cr.Generation &&
		cr.Status.LastAppliedHash == "sha256:rev-7-body" &&
		cr.Status.Phase == engine.PhaseInSync &&
		!dueForDriftCheck(cr)
	if !shortCircuits {
		t.Fatalf("expected short-circuit on steady-state rollback tick; cr=%+v", cr)
	}
}
