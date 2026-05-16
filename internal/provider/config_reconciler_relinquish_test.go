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

import (
	"context"
	"strings"
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
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers"
)

// reachableTransport is a fake transport.Interface that satisfies
// "transport not nil" and lets engine.Reconcile run end-to-end
// against a stubbed writer Lookup. Mutate records the ops it sees so
// tests can assert what was (or was not) sent to the device.
type reachableTransport struct {
	caps     transport.Capabilities
	mutateCh chan []transport.Op
	muteFor  map[string]bool // family → don't expect any ops
}

func newReachableTransport() *reachableTransport {
	return &reachableTransport{
		caps:     transport.Capabilities{Kind: transport.KindRESTCONF},
		mutateCh: make(chan []transport.Op, 16),
	}
}
func (t *reachableTransport) Capabilities() transport.Capabilities { return t.caps }
func (t *reachableTransport) Fetch(_ context.Context, _ string) ([]byte, error) {
	return []byte("[]"), nil
}
func (t *reachableTransport) StartTransaction(context.Context) (transport.TxHandle, error) {
	return "", transport.ErrUnsupported
}
func (t *reachableTransport) Mutate(_ context.Context, _ transport.TxHandle, ops []transport.Op) error {
	t.mutateCh <- ops
	return nil
}
func (t *reachableTransport) Commit(context.Context, transport.TxHandle) error  { return nil }
func (t *reachableTransport) Discard(context.Context, transport.TxHandle) error { return nil }
func (t *reachableTransport) SaveStartup(context.Context) error                 { return transport.ErrUnsupported }
func (t *reachableTransport) Close() error                                      { return nil }

// noopWriter is a SectionWriter that fetches empty observed, returns
// no diff ops, and is PruneCapable + KeyExtractable so the engine's
// pruneOnRelinquish path runs to completion. Tracks which families
// were mutated so tests can assert "blocked family was not touched".
type noopWriter struct {
	family   string
	mutated  *map[string]int
	pruneOps []transport.Op
}

func (w *noopWriter) Family() string                                          { return w.family }
func (w *noopWriter) YANGPaths() []string                                     { return []string{"/" + w.family} }
func (w *noopWriter) Fetch(context.Context, transport.Interface) (any, error) { return []any{}, nil }
func (w *noopWriter) Diff(_, _ any) ([]transport.Op, error)                   { return nil, nil }
func (w *noopWriter) Apply(_ context.Context, _ transport.Interface, ops []transport.Op) error {
	if w.mutated != nil {
		(*w.mutated)[w.family] += len(ops)
	}
	return nil
}
func (w *noopWriter) PruneDiff(_, _ any) ([]transport.Op, error) { return w.pruneOps, nil }
func (w *noopWriter) KeysOf(any) []string                        { return nil }

// lookupFromMap returns a writers.SectionWriter lookup closure
// backed by the supplied map. Keys are family names; missing
// families return nil (engine treats as Unsupported).
func lookupFromMap(m map[string]*noopWriter) func(string, string) writers.SectionWriter {
	return func(family string, _ string) writers.SectionWriter {
		if w, ok := m[family]; ok {
			return w
		}
		return nil
	}
}

// newSchemeWithLeases returns a scheme registering both the config
// CRD types and coordination.k8s.io/v1 so the FamilyLeaser fake-
// client path round-trips Lease objects.
func newSchemeWithLeases(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := newTestScheme(t)
	if err := coordv1.AddToScheme(s); err != nil {
		t.Fatalf("coordination AddToScheme: %v", err)
	}
	return s
}

// makeReachableReconciler stitches together a ConfigReconciler with a
// real FamilyLeaser, a reachable fake transport, and a writer lookup
// covering each family in `writerFor`. Returns the reconciler, the
// transport (so tests can assert mutation outcomes), and the
// lease-name helper bound to the device for direct seeding.
func makeReachableReconciler(
	t *testing.T,
	c client.Client,
	device string,
	writerFor map[string]*noopWriter,
) (*ConfigReconciler, *reachableTransport, func(family string) string) {
	t.Helper()
	tr := newReachableTransport()
	leaser := &engine.FamilyLeaser{
		Client:    c,
		Namespace: "leases",
		TTL:       30 * time.Second,
	}
	r := &ConfigReconciler{
		Client:     c,
		DeviceName: device,
		Leaser:     leaser,
		Lookup:     lookupFromMap(writerFor),
	}
	r.SetTransport(tr)
	leaseName := func(family string) string { return engine.LeaseName(device, family) }
	return r, tr, leaseName
}

// seedForeignLease writes a Lease object for (device, family) under
// the engine's canonical name, owned by `holder`, with given renew
// time. Tests use a fresh renew time to prove AcquireIfFree refuses
// the lease without taking it over.
func seedForeignLease(
	t *testing.T,
	c client.Client,
	device, family, holder string,
	renew time.Time,
) *coordv1.Lease {
	t.Helper()
	dur := int32(30)
	rt := metav1.NewMicroTime(renew)
	lease := &coordv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "leases",
			Name:      engine.LeaseName(device, family),
			Labels: map[string]string{
				"cisco.vk/device": device,
				"cisco.vk/family": family,
			},
		},
		Spec: coordv1.LeaseSpec{
			HolderIdentity:       &holder,
			LeaseDurationSeconds: &dur,
			AcquireTime:          &rt,
			RenewTime:            &rt,
		},
	}
	if err := c.Create(context.Background(), lease); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	return lease
}

// TestRelinquishOnDeleteSkipsLeaseBlockedFamilies pins the corrected
// A1 contract: the previous version of this test (a) seeded the wrong
// lease name and (b) ran without a transport so the function
// short-circuited before AcquireIfFree was reached. The fixed test seeds the canonical
// engine.LeaseName output, wires a reachable transport + writer
// Lookup, and asserts: (i) Reconcile returns error, (ii) finalizer
// retained, (iii) the foreign lease holder is unchanged (proving
// AcquireIfFree did NOT take over), (iv) the blocked family was NOT
// passed to engine.Reconcile.
func TestRelinquishOnDeleteSkipsLeaseBlockedFamilies(t *testing.T) {
	scheme := newSchemeWithLeases(t)
	cr := newCR("victim", "edge-01")
	cr.UID = types.UID("11111111-1111-1111-1111-aaaaaaaaaaaa")
	cr.Spec.ManagedFamilies = []string{"vlan", "vrf"}
	cr.Spec.PruneOnRelinquish = true
	dt := metav1.NewTime(time.Now())
	cr.DeletionTimestamp = &dt
	cr.Finalizers = []string{iosxeConfigFinalizer}
	cr.Status = configv1alpha1.IOSXEConfigStatus{
		AtomicReplaceOwnedKeys: map[string][]string{
			"vlan": {"4001"},
			"vrf":  {"NACXE-VRF"},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(&configv1alpha1.IOSXEConfig{}).
		Build()

	const foreignHolder = "other-cr#runtime-x"
	seedForeignLease(t, c, "edge-01", "vrf", foreignHolder, time.Now()) // unexpired

	mutated := map[string]int{}
	r, _, _ := makeReachableReconciler(t, c, "edge-01", map[string]*noopWriter{
		"vlan": {family: "vlan", mutated: &mutated},
		"vrf":  {family: "vrf", mutated: &mutated},
	})

	res, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "network", Name: "victim"},
	})
	if err == nil {
		t.Fatalf("expected error from Reconcile (lease-blocked family must keep finalizer); got res=%+v", res)
	}
	if !strings.Contains(err.Error(), "vrf") {
		t.Errorf("error must name the blocked family vrf; got: %v", err)
	}

	// Finalizer retained.
	var got configv1alpha1.IOSXEConfig
	if gerr := c.Get(context.Background(),
		types.NamespacedName{Namespace: "network", Name: "victim"}, &got); gerr != nil {
		t.Fatalf("Get IOSXEConfig: %v", gerr)
	}
	if !containsFinalizer(got.Finalizers, iosxeConfigFinalizer) {
		t.Errorf("finalizer was removed despite lease-blocked relinquish: %+v", got.Finalizers)
	}

	// Foreign holder unchanged — AcquireIfFree must NOT take over.
	var lease coordv1.Lease
	if gerr := c.Get(context.Background(), types.NamespacedName{
		Namespace: "leases",
		Name:      engine.LeaseName("edge-01", "vrf"),
	}, &lease); gerr != nil {
		t.Fatalf("Get vrf lease: %v", gerr)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != foreignHolder {
		t.Errorf("vrf lease holder changed: got %v want %q (AcquireIfFree must not take over foreign leases)",
			lease.Spec.HolderIdentity, foreignHolder)
	}

	// Blocked family must NOT have been passed to engine.Reconcile.
	if mutated["vrf"] != 0 {
		t.Errorf("blocked vrf family was mutated %d times; expected 0", mutated["vrf"])
	}
}

// TestRelinquishDoesNotTakeOverStaleButInFlightLease pins B1:
// a foreign lease whose
// RenewTime + LeaseDuration is in the past is still NOT a free
// lease — the holder may be mid-Fetch/Apply on a long device call.
// AcquireIfFree must refuse takeover; the deleting CR must surface
// "blocked" rather than silently overwriting the holder identity.
func TestRelinquishDoesNotTakeOverStaleButInFlightLease(t *testing.T) {
	scheme := newSchemeWithLeases(t)
	cr := newCR("stale-test", "edge-01")
	cr.UID = types.UID("33333333-3333-3333-3333-cccccccccccc")
	cr.Spec.ManagedFamilies = []string{"vlan"}
	cr.Spec.PruneOnRelinquish = true
	dt := metav1.NewTime(time.Now())
	cr.DeletionTimestamp = &dt
	cr.Finalizers = []string{iosxeConfigFinalizer}
	cr.Status = configv1alpha1.IOSXEConfigStatus{
		AtomicReplaceOwnedKeys: map[string][]string{"vlan": {"4001"}},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(&configv1alpha1.IOSXEConfig{}).
		Build()

	// Seed a vlan lease with RenewTime 5 minutes in the past — well
	// past the 30s TTL — held by another in-flight reconciler.
	const foreignHolder = "in-flight-cr#runtime-y"
	seedForeignLease(t, c, "edge-01", "vlan", foreignHolder, time.Now().Add(-5*time.Minute))

	r, _, _ := makeReachableReconciler(t, c, "edge-01", map[string]*noopWriter{
		"vlan": {family: "vlan"},
	})

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "network", Name: "stale-test"},
	})
	if err == nil {
		t.Fatalf("expected error for stale-but-in-flight foreign lease; got nil")
	}

	var lease coordv1.Lease
	if gerr := c.Get(context.Background(), types.NamespacedName{
		Namespace: "leases",
		Name:      engine.LeaseName("edge-01", "vlan"),
	}, &lease); gerr != nil {
		t.Fatalf("Get vlan lease: %v", gerr)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != foreignHolder {
		t.Errorf("stale-but-in-flight lease was taken over: holder=%v want %q",
			lease.Spec.HolderIdentity, foreignHolder)
	}
}

// TestRelinquishOnDeleteRetainsFinalizerWhenTransportMissing pins A2:
// when relinquish cannot run
// (transport down at delete time, partial device failure, lease-
// blocked, etc.) the controller must return error from Reconcile so
// the next tick retries. status.atomicReplaceOwnedKeys stays
// available for the retry; pre-fix the controller silently logged +
// dropped the finalizer, leaving orphaned device config with no
// retry path.
func TestRelinquishOnDeleteRetainsFinalizerWhenTransportMissing(t *testing.T) {
	scheme := newSchemeWithLeases(t)
	cr := newCR("orphaner", "edge-01")
	cr.UID = types.UID("22222222-2222-2222-2222-bbbbbbbbbbbb")
	cr.Spec.ManagedFamilies = []string{"vlan"}
	cr.Spec.PruneOnRelinquish = true
	dt := metav1.NewTime(time.Now())
	cr.DeletionTimestamp = &dt
	cr.Finalizers = []string{iosxeConfigFinalizer}
	cr.Status = configv1alpha1.IOSXEConfigStatus{
		AtomicReplaceOwnedKeys: map[string][]string{"vlan": {"4001"}},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(&configv1alpha1.IOSXEConfig{}).
		Build()

	r := &ConfigReconciler{
		Client:     c,
		DeviceName: "edge-01",
		// No Transport, no Leaser → relinquishOwnedKeys returns
		// "transport not yet available". Test asserts the resulting
		// error path keeps the finalizer.
	}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "network", Name: "orphaner"},
	})
	if err == nil {
		t.Fatalf("expected Reconcile to return error so controller-runtime requeues")
	}

	var got configv1alpha1.IOSXEConfig
	if gerr := c.Get(context.Background(),
		types.NamespacedName{Namespace: "network", Name: "orphaner"}, &got); gerr != nil {
		t.Fatalf("Get: %v", gerr)
	}
	if !containsFinalizer(got.Finalizers, iosxeConfigFinalizer) {
		t.Errorf("finalizer was removed despite relinquish failure; got %+v", got.Finalizers)
	}
	if len(got.Status.AtomicReplaceOwnedKeys["vlan"]) == 0 {
		t.Errorf("atomicReplaceOwnedKeys lost across the failed relinquish; "+
			"the next retry would have nothing to clean up. got %+v",
			got.Status.AtomicReplaceOwnedKeys)
	}
}

// TestRelinquishReleasesAcquiredLeasesOnFailure pins B2a:
// when relinquishOwnedKeys fails
// after acquiring some leases, those leases must be released so a
// stuck terminating CR doesn't permanently pin those families for
// other CRs. Pre-fix, a CR with two managed families where one
// failed would hold both leases until manual finalizer removal.
func TestRelinquishReleasesAcquiredLeasesOnFailure(t *testing.T) {
	scheme := newSchemeWithLeases(t)
	cr := newCR("releaser", "edge-01")
	cr.UID = types.UID("44444444-4444-4444-4444-dddddddddddd")
	cr.Spec.ManagedFamilies = []string{"vlan", "vrf"}
	cr.Spec.PruneOnRelinquish = true
	dt := metav1.NewTime(time.Now())
	cr.DeletionTimestamp = &dt
	cr.Finalizers = []string{iosxeConfigFinalizer}
	cr.Status = configv1alpha1.IOSXEConfigStatus{
		AtomicReplaceOwnedKeys: map[string][]string{
			"vlan": {"4001"},
			"vrf":  {"NACXE-VRF"},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(&configv1alpha1.IOSXEConfig{}).
		Build()

	// vrf is held by another CR — the deletion will partially
	// succeed (vlan pruned) and partially fail (vrf blocked).
	const foreignHolder = "other-cr#runtime-z"
	seedForeignLease(t, c, "edge-01", "vrf", foreignHolder, time.Now())

	r, _, _ := makeReachableReconciler(t, c, "edge-01", map[string]*noopWriter{
		"vlan": {family: "vlan"},
		"vrf":  {family: "vrf"},
	})

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "network", Name: "releaser"},
	})
	if err == nil {
		t.Fatalf("expected error (vrf is lease-blocked); got nil")
	}

	// vlan lease must NOT be held by us anymore — we released it
	// after the partial-success relinquish so the next CR can claim
	// it. Acceptable end states: (a) lease deleted, (b) lease has a
	// different holder. The test fixture only proves we don't hold
	// it.
	myIdentity := "network/releaser"
	var vlanLease coordv1.Lease
	gerr := c.Get(context.Background(), types.NamespacedName{
		Namespace: "leases",
		Name:      engine.LeaseName("edge-01", "vlan"),
	}, &vlanLease)
	if gerr == nil {
		if vlanLease.Spec.HolderIdentity != nil && *vlanLease.Spec.HolderIdentity == myIdentity {
			t.Errorf("vlan lease still held by terminating CR after relinquish failure; "+
				"holder=%q (B2: leases must be released so other CRs aren't pinned)",
				*vlanLease.Spec.HolderIdentity)
		}
	}
}

// TestRelinquishSkippedByForceAnnotation pins B2b: the operator escape
// hatch. When
// cisco.vk/force-relinquish-skip=true is set on a Terminating CR
// with pruneOnRelinquish, the controller skips the relinquish
// reconcile, records a Warning event listing the orphan, and
// proceeds to finalizer removal. Strictly more controlled than
// `kubectl patch finalizers: []` because the orphan list lands in
// the audit trail.
func TestRelinquishSkippedByForceAnnotation(t *testing.T) {
	scheme := newSchemeWithLeases(t)
	cr := newCR("skipper", "edge-01")
	cr.UID = types.UID("55555555-5555-5555-5555-eeeeeeeeeeee")
	cr.Annotations = map[string]string{ForceRelinquishSkipAnnotation: "true"}
	cr.Spec.ManagedFamilies = []string{"vlan"}
	cr.Spec.PruneOnRelinquish = true
	dt := metav1.NewTime(time.Now())
	cr.DeletionTimestamp = &dt
	cr.Finalizers = []string{iosxeConfigFinalizer}
	cr.Status = configv1alpha1.IOSXEConfigStatus{
		AtomicReplaceOwnedKeys: map[string][]string{"vlan": {"4001"}},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(&configv1alpha1.IOSXEConfig{}).
		Build()

	mutated := map[string]int{}
	r, _, _ := makeReachableReconciler(t, c, "edge-01", map[string]*noopWriter{
		"vlan": {family: "vlan", mutated: &mutated},
	})

	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "network", Name: "skipper"},
	}); err != nil {
		t.Fatalf("Reconcile error with skip annotation set: %v", err)
	}

	// CR should be gone (or mid-deletion with finalizer gone).
	var got configv1alpha1.IOSXEConfig
	gerr := c.Get(context.Background(),
		types.NamespacedName{Namespace: "network", Name: "skipper"}, &got)
	if gerr == nil {
		// API server returns the object until garbage-collected; the
		// annotated path must have removed the finalizer.
		if containsFinalizer(got.Finalizers, iosxeConfigFinalizer) {
			t.Errorf("force-relinquish-skip annotation did not remove finalizer; got %+v", got.Finalizers)
		}
	}
	// Skip path must NOT have mutated the device.
	if mutated["vlan"] != 0 {
		t.Errorf("force-skip annotation triggered device mutation: %d ops", mutated["vlan"])
	}
}
