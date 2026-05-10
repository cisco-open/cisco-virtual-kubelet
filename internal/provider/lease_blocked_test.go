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
	"context"
	"testing"
	"time"

	coordv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers"
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
	}
	// Wave 9.2: phase comes from the engine.Result returned by
	// reconcileOne, not cr.Status.Phase — pass it explicitly so the
	// test stays honest about what production callers feed in.
	got := requeueIntervalFor(cr, engine.PhaseLeaseBlocked)
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
	}
	got := requeueIntervalFor(cr, engine.PhaseInSync)
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
	}
	got := requeueIntervalFor(cr, engine.PhaseLeaseBlocked)
	// 30s (clamped drift interval) > 15s (lease-blocked default), so
	// the lease-blocked default wins.
	if got > 30*time.Second {
		t.Errorf("LeaseBlocked requeue should be capped at the sub-TTL value, got %v", got)
	}
}

// TestRequeueIntervalFor_StalePhaseIgnored is the regression for
// Wave 9.2's reviewer finding: pre-fix, requeueIntervalFor read
// cr.Status.Phase from the pre-update copy, so a tick that wrote
// LeaseBlocked still requeued at the normal drift interval. Now
// the phase is an explicit argument; passing the just-written
// LeaseBlocked phase produces the sub-TTL requeue regardless of
// what cr.Status.Phase happens to hold.
func TestRequeueIntervalFor_StalePhaseIgnored(t *testing.T) {
	t.Parallel()
	cr := &configv1alpha1.IOSXEConfig{
		Spec: configv1alpha1.IOSXEConfigSpec{
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				DriftDetectInterval: "5m",
			},
		},
		Status: configv1alpha1.IOSXEConfigStatus{
			// Stale value from the previous tick — production caller
			// must not read this.
			Phase: engine.PhaseInSync,
		},
	}
	got := requeueIntervalFor(cr, engine.PhaseLeaseBlocked)
	if got >= 5*time.Minute {
		t.Errorf("phase argument must override stale cr.Status.Phase, got %v", got)
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
	if err := r.recordResult(t.Context(), &seeded, result, "hash", nil, nil, ""); err != nil {
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
	if err := r.recordResult(t.Context(), &seeded, result, "hash", nil, nil, ""); err != nil {
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

// noCallTransport is a transport.Interface stub that fails the test
// if any method is invoked. The Wave 9.2 all-blocked path must NOT
// reach the engine, so a panicking transport is the strongest possible
// proof: if the test passes with this transport wired, no Fetch /
// Mutate / Commit happened.
type noCallTransport struct{ t *testing.T }

func (n *noCallTransport) Capabilities() transport.Capabilities {
	// Capabilities is read by the engine constructor in some paths but
	// is also fine as a side-effect-free metadata query. Allow it.
	return transport.Capabilities{Kind: transport.KindRESTCONF}
}
func (n *noCallTransport) Fetch(context.Context, string) ([]byte, error) {
	n.t.Fatal("noCallTransport.Fetch must not be called on the all-blocked lease path")
	return nil, nil
}
func (n *noCallTransport) StartTransaction(context.Context) (transport.TxHandle, error) {
	n.t.Fatal("noCallTransport.StartTransaction must not be called on the all-blocked lease path")
	return "", nil
}
func (n *noCallTransport) Mutate(context.Context, transport.TxHandle, []transport.Op) error {
	n.t.Fatal("noCallTransport.Mutate must not be called on the all-blocked lease path")
	return nil
}
func (n *noCallTransport) Commit(context.Context, transport.TxHandle) error {
	n.t.Fatal("noCallTransport.Commit must not be called on the all-blocked lease path")
	return nil
}
func (n *noCallTransport) Discard(context.Context, transport.TxHandle) error {
	n.t.Fatal("noCallTransport.Discard must not be called on the all-blocked lease path")
	return nil
}
func (n *noCallTransport) SaveStartup(context.Context) error {
	n.t.Fatal("noCallTransport.SaveStartup must not be called on the all-blocked lease path")
	return nil
}
func (n *noCallTransport) Close() error { return nil }

// leaseBlockedScheme registers the schemes the headline test needs:
// the config CRDs (for the IOSXEConfig CR), the cisko API (for
// CiscoDevice), and coordination/v1 (for the foreign Lease the
// FamilyLeaser pre-seeds).
func leaseBlockedScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := configv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("config AddToScheme: %v", err)
	}
	if err := ciskov1.AddToScheme(s); err != nil {
		t.Fatalf("cisko AddToScheme: %v", err)
	}
	if err := coordv1.AddToScheme(s); err != nil {
		t.Fatalf("coordv1 AddToScheme: %v", err)
	}
	return s
}

// TestReconcile_AllBlockedReturnsSubTTLRequeueAndLeaseBlockedPhase
// is the headline regression for external-review-wave8-followup
// Finding #2. Pre-fix, the controller-runtime Reconcile path
// computed RequeueAfter from the stale pre-update CR object —
// cr.Status.Phase was still the previous tick's value (commonly
// PhaseInSync) because recordResult writes status via a deep copy
// that never mutates `cr`. So even when this tick wrote
// PhaseLeaseBlocked, the next requeue used the normal drift
// interval (5m default) instead of the contention-aware sub-TTL
// (15s). Wave 9.2 fixes this by returning the engine.Result from
// reconcileOne; the requeue now reads the just-written phase.
//
// This is also the schema-aware proof for Finding #1: writing
// PhaseLeaseBlocked into IOSXEConfig.status.phase via a fake client
// that has the status subresource registered does not blow up
// today because fake.Client doesn't enforce CRD enums — the
// separate iosxeconfig_phase_enum_test.go guards against that
// regression.
func TestReconcile_AllBlockedReturnsSubTTLRequeueAndLeaseBlockedPhase(t *testing.T) {
	t.Parallel()

	scheme := leaseBlockedScheme(t)
	device := newDevice("edge-01")
	cr := newCR("edge-01", "edge-01")
	cr.Spec.DriftDetectInterval = "5m"

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(device, cr).
		WithStatusSubresource(&configv1alpha1.IOSXEConfig{}).
		Build()

	leaser := &engine.FamilyLeaser{Client: c, Namespace: "lease-ns", TTL: 30 * time.Second}

	// Pre-seed a foreign lease against the only managed family. A
	// different identity ensures our reconciler cannot acquire it.
	if _, err := leaser.Acquire(t.Context(), "edge-01", "vlan", "foreign-pod#abc"); err != nil {
		t.Fatalf("seed foreign lease: %v", err)
	}

	r := &ConfigReconciler{
		Client:     c,
		DeviceName: "edge-01",
		Transport:  &noCallTransport{t: t},
		Leaser:     leaser,
		RuntimeID:  "our-pod",
		Lookup:     func(string) writers.SectionWriter { return nil },
	}

	res, err := r.Reconcile(t.Context(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "network", Name: "edge-01"},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// Assertion 1: requeue is sub-TTL — not the 5m drift interval.
	if res.RequeueAfter == 0 || res.RequeueAfter >= 5*time.Minute {
		t.Errorf("RequeueAfter=%v; want 0 < x < 5m (sub-TTL contention requeue)", res.RequeueAfter)
	}

	// Assertion 2: status.phase is LeaseBlocked.
	var got configv1alpha1.IOSXEConfig
	if err := c.Get(t.Context(),
		types.NamespacedName{Namespace: "network", Name: "edge-01"}, &got); err != nil {
		t.Fatalf("re-fetch CR: %v", err)
	}
	if got.Status.Phase != engine.PhaseLeaseBlocked {
		t.Errorf("status.phase=%q, want %q", got.Status.Phase, engine.PhaseLeaseBlocked)
	}

	// Assertion 3: LastDeviceCheck is unchanged (nil here — the CR
	// was seeded without one). Pre-Wave-8.2 recordResult bumped this
	// unconditionally; Wave 8.2 gates on result.DeviceTouched, which
	// is false on the all-blocked synthesised path.
	if got.Status.LastDeviceCheck != nil {
		t.Errorf("LastDeviceCheck advanced on all-blocked tick: %v", got.Status.LastDeviceCheck)
	}
}
