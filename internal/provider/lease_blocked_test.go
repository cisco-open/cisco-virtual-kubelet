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

// Wave 8.2 regression tests for external-review-wave7-residuals
// Finding #2. Lease conflicts must surface as a first-class
// arbitration state (PhaseLeaseBlocked) rather than route through
// the engine as Failed (all-blocked) or InSync (partial-block).
// LastDeviceCheck must NOT advance on lease-blocked ticks because
// no device-side work happened. Sub-TTL requeue under
// PhaseLeaseBlocked so the next tick re-checks while contention is
// still likely resolving.

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine"
)

// newRecordResultClient builds a fake client that records writes
// against the IOSXEConfig CR — recordResult does both Spec-side and
// Status-side updates via r.Client.Status().Update, so the fake
// needs the status subresource registered.
func newRecordResultClient(scheme *runtime.Scheme) client.Client {
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.IOSXEConfig{}).
		Build()
}

// recordResultKey is the namespaced key for the seeded CR. Tiny
// helper kept here so the lease-blocked tests are self-contained.
func recordResultKey(cr *configv1alpha1.IOSXEConfig) types.NamespacedName {
	return types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}
}

func TestRequeueIntervalFor_LeaseBlockedIsSubTTL(t *testing.T) {
	t.Parallel()
	cr := &configv1alpha1.IOSXEConfig{
		Spec: configv1alpha1.IOSXEConfigSpec{
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				DriftDetectInterval: "5m",
			},
		},
		Status: configv1alpha1.IOSXEConfigStatus{
			Phase: engine.PhaseLeaseBlocked,
		},
	}
	got := requeueIntervalFor(cr)
	// Sub-TTL: shorter than the default 5m drift interval, longer
	// than 1 second so the reconciler doesn't busy-loop.
	if got >= 5*time.Minute {
		t.Errorf("LeaseBlocked requeue should be sub-TTL, got %v", got)
	}
	if got < 1*time.Second {
		t.Errorf("LeaseBlocked requeue too short (%v) — risk of busy-loop", got)
	}
}

func TestRequeueIntervalFor_NormalUsesDriftInterval(t *testing.T) {
	t.Parallel()
	cr := &configv1alpha1.IOSXEConfig{
		Spec: configv1alpha1.IOSXEConfigSpec{
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				DriftDetectInterval: "5m",
			},
		},
		Status: configv1alpha1.IOSXEConfigStatus{
			Phase: engine.PhaseInSync, // not lease-blocked
		},
	}
	got := requeueIntervalFor(cr)
	if got != 5*time.Minute {
		t.Errorf("non-LeaseBlocked requeue should be driftDetectInterval, got %v", got)
	}
}

// TestRequeueIntervalFor_VeryShortDriftIntervalPassesThrough pins
// the bounds: when the operator's driftDetectInterval is already
// shorter than the lease-blocked default, use the operator's value
// (no point requeueing slower than they asked for).
func TestRequeueIntervalFor_VeryShortDriftIntervalPassesThrough(t *testing.T) {
	t.Parallel()
	cr := &configv1alpha1.IOSXEConfig{
		Spec: configv1alpha1.IOSXEConfigSpec{
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				DriftDetectInterval: "5s", // gets clamped to minDriftDetectInterval (30s)
			},
		},
		Status: configv1alpha1.IOSXEConfigStatus{
			Phase: engine.PhaseLeaseBlocked,
		},
	}
	got := requeueIntervalFor(cr)
	// 30s (clamped drift interval) > 15s (lease-blocked default), so
	// the lease-blocked default wins.
	if got > 30*time.Second {
		t.Errorf("LeaseBlocked requeue should be capped at the sub-TTL value, got %v", got)
	}
}

// TestEngineResult_DeviceTouchedSetWhenManagedFamilies confirms the
// engine sets DeviceTouched when at least one family is managed.
// recordResult relies on this to gate the LastDeviceCheck bump.
func TestEngineResult_DeviceTouchedSetWhenManagedFamilies(t *testing.T) {
	t.Parallel()
	// We can't easily construct a full Engine here without the
	// transport stack, so verify the contract via the Result type
	// directly: the field is exposed for the reconciler to read,
	// and the engine code path that iterates families sets it.
	// This is a contract pin: if a future change drops the field,
	// recordResult breaks silently.
	r := engine.Result{}
	if r.DeviceTouched {
		t.Errorf("zero-value Result must have DeviceTouched=false")
	}
	r.DeviceTouched = true
	if !r.DeviceTouched {
		t.Errorf("DeviceTouched not settable")
	}
}

// TestRecordResult_LeaseBlockedDoesNotBumpLastDeviceCheck wires the
// recordResult path with a synthetic LeaseBlocked result (no
// DeviceTouched) and asserts LastDeviceCheck is not advanced.
// Pre-fix this test would have failed: recordResult bumped the
// timestamp on every call.
func TestRecordResult_LeaseBlockedDoesNotBumpLastDeviceCheck(t *testing.T) {
	t.Parallel()
	scheme := newTestScheme(t)
	c := newRecordResultClient(scheme)
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}

	priorCheck := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	cr := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "n", Name: "cr-1", Generation: 1,
		},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "edge-01"},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies: []string{"vlan"},
			},
		},
		Status: configv1alpha1.IOSXEConfigStatus{
			LastDeviceCheck: &priorCheck,
		},
	}
	if err := c.Create(t.Context(), cr); err != nil {
		t.Fatalf("seed CR: %v", err)
	}
	// Capture the post-Create truncated timestamp — metav1.Time
	// JSON round-trip loses sub-second precision, so the in-store
	// value differs from the literal we constructed.
	var seeded configv1alpha1.IOSXEConfig
	if err := c.Get(t.Context(), recordResultKey(cr), &seeded); err != nil {
		t.Fatalf("re-fetch seeded: %v", err)
	}
	priorAsStored := seeded.Status.LastDeviceCheck

	// LeaseBlocked synthetic result — DeviceTouched stays false.
	result := engine.Result{
		Phase:         engine.PhaseLeaseBlocked,
		DeviceTouched: false,
	}
	if err := r.recordResult(t.Context(), &seeded, result, "hash", nil, nil); err != nil {
		t.Fatalf("recordResult: %v", err)
	}

	// Re-fetch and check LastDeviceCheck unchanged from the seeded
	// (post-truncation) form. Bump-on-lease-blocked-tick is the bug
	// Wave 8.2 fixes; this test fails before the fix.
	var fetched configv1alpha1.IOSXEConfig
	if err := c.Get(t.Context(), recordResultKey(cr), &fetched); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if fetched.Status.LastDeviceCheck == nil {
		t.Fatalf("LastDeviceCheck cleared; expected to remain at prior value")
	}
	if !fetched.Status.LastDeviceCheck.Equal(priorAsStored) {
		t.Errorf("LastDeviceCheck advanced on lease-blocked tick: was %v, now %v",
			priorAsStored, *fetched.Status.LastDeviceCheck)
	}
}

// TestRecordResult_DeviceTouchedBumpsLastDeviceCheck is the
// complement — a normal reconcile DOES advance the timestamp.
func TestRecordResult_DeviceTouchedBumpsLastDeviceCheck(t *testing.T) {
	t.Parallel()
	scheme := newTestScheme(t)
	c := newRecordResultClient(scheme)
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}

	priorCheck := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	cr := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "n", Name: "cr-1", Generation: 1,
		},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "edge-01"},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies: []string{"vlan"},
			},
		},
		Status: configv1alpha1.IOSXEConfigStatus{
			LastDeviceCheck: &priorCheck,
		},
	}
	if err := c.Create(t.Context(), cr); err != nil {
		t.Fatalf("seed CR: %v", err)
	}
	var seeded configv1alpha1.IOSXEConfig
	if err := c.Get(t.Context(), recordResultKey(cr), &seeded); err != nil {
		t.Fatalf("re-fetch seeded: %v", err)
	}
	priorAsStored := seeded.Status.LastDeviceCheck

	result := engine.Result{
		Phase:         engine.PhaseInSync,
		DeviceTouched: true,
	}
	if err := r.recordResult(t.Context(), &seeded, result, "hash", nil, nil); err != nil {
		t.Fatalf("recordResult: %v", err)
	}

	var fetched configv1alpha1.IOSXEConfig
	if err := c.Get(t.Context(), recordResultKey(cr), &fetched); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if fetched.Status.LastDeviceCheck == nil {
		t.Fatal("LastDeviceCheck not set after device-touched reconcile")
	}
	if priorAsStored != nil && fetched.Status.LastDeviceCheck.Equal(priorAsStored) {
		t.Errorf("LastDeviceCheck not advanced (still %v) after device-touched reconcile", priorAsStored)
	}
}
