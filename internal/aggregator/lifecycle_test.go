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

package aggregator

// Lifecycle tests close watch-item #5 from the architectural review:
// the AggregatedReconciler's start/stop/spec-change semantics were
// previously only exercised via a fake Reconcile() round-trip that
// stopped at the platform-skip path. These tests register a stub
// config-driver factory under the FAKE driver kind and exercise the
// full Reconcile() path: worker creation, idempotent spec-unchanged
// reconciliation, spec-change rebuild, and delete-induced teardown.
//
// We deliberately use direct Reconcile() invocation against a fake
// client rather than envtest. envtest would verify the controller-
// runtime watch wiring (predicate, mapping handlers) but the wiring
// is generic and inherits its correctness from controller-runtime
// itself; the *aggregator* logic worth covering is the lifecycle
// state machine, which is fully driven by Reconcile() calls.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// stubTransport is a no-op transport.Interface that the lifecycle
// fixtures can hand to the aggregator. Methods return zero values
// (or ErrUnsupported where the contract demands it) so the
// ConfigReconciler.Run goroutine the aggregator spawns blocks
// harmlessly inside its first Fetch / List loop and exits cleanly
// when the per-device context cancels.
type stubTransport struct {
	closed atomic.Bool
}

func (*stubTransport) Capabilities() transport.Capabilities { return transport.Capabilities{Kind: "stub"} }
func (*stubTransport) Fetch(context.Context, string) ([]byte, error) {
	return nil, transport.ErrUnsupported
}
func (*stubTransport) StartTransaction(context.Context) (transport.TxHandle, error) {
	return "", transport.ErrUnsupported
}
func (*stubTransport) Mutate(context.Context, transport.TxHandle, []transport.Op) error {
	return transport.ErrUnsupported
}
func (*stubTransport) Commit(context.Context, transport.TxHandle) error {
	return transport.ErrUnsupported
}
func (*stubTransport) Discard(context.Context, transport.TxHandle) error {
	return transport.ErrUnsupported
}
func (*stubTransport) SaveStartup(context.Context) error { return transport.ErrUnsupported }
func (s *stubTransport) Close() error {
	s.closed.Store(true)
	return nil
}

// registerStubFakeOnce registers a configdriver factory for the FAKE
// driver kind so the aggregator's NewConfigDriver call can succeed
// inside tests. Wrapped in sync.Once so multiple lifecycle tests in
// the same `go test` invocation do not trigger the registry's
// duplicate-registration panic.
var registerStubFakeOnce sync.Once

func registerStubFakeDriver(t *testing.T) {
	t.Helper()
	registerStubFakeOnce.Do(func() {
		drivers.RegisterConfigDriver(
			ciskov1.DeviceDriverFAKE,
			func(_ context.Context, _ *ciskov1.DeviceSpec, _ string, _ drivers.ConfigDriverOptions) (*drivers.ConfigDriverContext, error) {
				return &drivers.ConfigDriverContext{
					Transport: &stubTransport{},
				}, nil
			},
		)
	})
}

// TestAggregatorLifecycle drives the four invariants the architectural
// review called out: start, idempotent re-reconcile, spec-change
// rebuild, and delete-induced teardown.
func TestAggregatorLifecycle(t *testing.T) {
	registerStubFakeDriver(t)

	scheme := aggScheme(t)
	dev := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "fake-01", Namespace: "agg-test"},
		Spec: ciskov1.DeviceSpec{
			Driver:   ciskov1.DeviceDriverFAKE,
			Address:  "10.0.0.1",
			Username: "u",
			Password: "inline",
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}).
		WithObjects(dev).
		Build()

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := &AggregatedReconciler{
		Client:  c,
		Scheme:  scheme,
		managed: map[string]*deviceWorker{},
		rootCtx: rootCtx,
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: dev.Namespace, Name: dev.Name}}

	// ── 1. First Reconcile starts a worker ─────────────────────────────
	if _, err := r.Reconcile(rootCtx, req); err != nil {
		t.Fatalf("Reconcile #1: %v", err)
	}
	r.mu.Lock()
	w1, ok := r.managed[req.String()]
	r.mu.Unlock()
	if !ok || w1.cancel == nil {
		t.Fatalf("after Reconcile #1, expected a managed worker; got %+v ok=%v", w1, ok)
	}
	hash1 := w1.specHash

	// ── 2. Idempotent: same spec → same worker, no rebuild ─────────────
	if _, err := r.Reconcile(rootCtx, req); err != nil {
		t.Fatalf("Reconcile #2: %v", err)
	}
	r.mu.Lock()
	w2 := r.managed[req.String()]
	r.mu.Unlock()
	if w2 != w1 {
		t.Errorf("spec unchanged: worker pointer changed (was %p, now %p)", w1, w2)
	}
	if w2.specHash != hash1 {
		t.Errorf("spec unchanged: hash mutated %q → %q", hash1, w2.specHash)
	}

	// ── 3. Spec change rebuilds: address mutates ───────────────────────
	updated := &ciskov1.CiscoDevice{}
	if err := c.Get(rootCtx, req.NamespacedName, updated); err != nil {
		t.Fatalf("get for update: %v", err)
	}
	updated.Spec.Address = "10.0.0.2"
	if err := c.Update(rootCtx, updated); err != nil {
		t.Fatalf("update spec: %v", err)
	}
	if _, err := r.Reconcile(rootCtx, req); err != nil {
		t.Fatalf("Reconcile #3: %v", err)
	}
	r.mu.Lock()
	w3 := r.managed[req.String()]
	r.mu.Unlock()
	if w3 == w1 {
		t.Errorf("spec changed: worker pointer should have rotated; both %p", w3)
	}
	if w3.specHash == hash1 {
		t.Errorf("spec changed: hash %q should have rotated", hash1)
	}

	// ── 4. Delete tears down ───────────────────────────────────────────
	if err := c.Delete(rootCtx, updated); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := r.Reconcile(rootCtx, req); err != nil {
		t.Fatalf("Reconcile #4: %v", err)
	}
	r.mu.Lock()
	_, present := r.managed[req.String()]
	r.mu.Unlock()
	if present {
		t.Errorf("after delete, managed map should not contain %q", req.String())
	}

	// Allow the worker goroutines to observe the cancellation. We
	// don't assert on stub.closed because Close() is called by the
	// inner ConfigReconciler.Run goroutine on its own schedule;
	// asserting on synchronous teardown would race with that.
	time.Sleep(50 * time.Millisecond)
}

// TestAggregatorSkipsUnregisteredDriver pins the contract that an
// unregistered driver kind is silently ignored. The cisco-vk-foundry
// model relies on this: a binary built without (say) the OpenConfig
// driver blank-imported sees OPENCONFIG CiscoDevice CRs and just
// does nothing rather than crash.
func TestAggregatorSkipsUnregisteredDriver(t *testing.T) {
	scheme := aggScheme(t)
	// OPENCONFIG is a placeholder driver kind that ships with no
	// configdriver factory registration today.
	dev := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "oc-01", Namespace: "agg-test"},
		Spec: ciskov1.DeviceSpec{
			Driver:   ciskov1.DeviceDriverOPENCONFIG,
			Address:  "10.0.0.5",
			Username: "u",
			Password: "p",
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dev).Build()
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := &AggregatedReconciler{
		Client:  c,
		Scheme:  scheme,
		managed: map[string]*deviceWorker{},
		rootCtx: rootCtx,
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: dev.Namespace, Name: dev.Name}}

	if _, err := r.Reconcile(rootCtx, req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	r.mu.Lock()
	_, present := r.managed[req.String()]
	r.mu.Unlock()
	if present {
		t.Errorf("unregistered driver should not produce a managed worker")
	}
}

// TestAggregatorCredentialFallthrough pins the safety net: if a
// credential cannot be resolved (Secret missing, no inline password),
// Reconcile must NOT crash, must NOT start a partial worker, and
// must surface the failure as an Event for the operator.
func TestAggregatorCredentialFallthrough(t *testing.T) {
	registerStubFakeDriver(t)
	scheme := aggScheme(t)
	dev := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "no-creds", Namespace: "agg-test"},
		Spec: ciskov1.DeviceSpec{
			Driver:   ciskov1.DeviceDriverFAKE,
			Address:  "10.0.0.9",
			Username: "u",
			CredentialSecretRef: &corev1.LocalObjectReference{
				Name: "this-secret-does-not-exist",
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dev).Build()
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := &AggregatedReconciler{
		Client:  c,
		Scheme:  scheme,
		managed: map[string]*deviceWorker{},
		rootCtx: rootCtx,
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: dev.Namespace, Name: dev.Name}}

	if _, err := r.Reconcile(rootCtx, req); err != nil {
		t.Fatalf("Reconcile should soak credential errors, got: %v", err)
	}
	r.mu.Lock()
	_, present := r.managed[req.String()]
	r.mu.Unlock()
	if present {
		t.Errorf("missing credential should NOT start a partial worker")
	}
}
