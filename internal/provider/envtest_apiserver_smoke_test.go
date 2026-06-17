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

//go:build envtest
// +build envtest

package provider

// Real-apiserver smoke tests for the recurring fake-client blind spots
// flagged across W7R-1, FU-2, and W8FU-1. These run against a real
// kube-apiserver + etcd brought up by sigs.k8s.io/controller-runtime/
// pkg/envtest, so admission webhooks, OpenAPI enum validation, and
// DNS-1123-subdomain name validation all execute exactly as a
// production cluster would.
//
// Build tag `envtest` keeps these out of normal `go test ./...` runs
// because they require KUBEBUILDER_ASSETS to point at downloaded
// kube-apiserver/etcd binaries. Run with:
//
//   KUBEBUILDER_ASSETS="$(setup-envtest use 1.30 -p path)" \
//     go test -tags envtest -count=1 ./internal/provider/
//
// The Makefile target `test-envtest` (when added) is the canonical
// invocation.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
)

// findCRDPathEnvtest walks up from the test's working directory until
// it finds config/crd. envtest needs the directory containing the
// generated CRD YAMLs so the apiserver can install them at startup.
func findCRDPathEnvtest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		candidate := filepath.Join(dir, "config", "crd")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate config/crd above %s", dir)
		}
		dir = parent
	}
}

// startEnvtest brings up the apiserver+etcd, installs the project
// CRDs, registers our scheme, and returns a client + stop func.
// Failures during start are fatal — the test cannot continue without
// a real apiserver and falling back to fake.Client would defeat the
// purpose.
func startEnvtest(t *testing.T) (client.Client, func()) {
	t.Helper()
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{findCRDPathEnvtest(t)},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("envtest start: %v (is KUBEBUILDER_ASSETS set?)", err)
	}
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1alpha1.AddToScheme(scheme))
	utilruntime.Must(opsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(ciskov1.AddToScheme(scheme))
	utilruntime.Must(coordv1.AddToScheme(scheme))
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		_ = env.Stop()
		t.Fatalf("client: %v", err)
	}
	return c, func() { _ = env.Stop() }
}

// envtestNamespace creates a namespace via the real apiserver. Tests
// share one namespace per envtest run; the fixture is small enough
// that a single namespace is fine.
func envtestNamespace(t *testing.T, c client.Client, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}); err != nil {
		t.Fatalf("create namespace %q: %v", name, err)
	}
}

// TestEnvtest_StatusPhaseLeaseBlockedAccepted is the W8FU-1 smoke.
// Wave 9.1 added LeaseBlocked to the kubebuilder enum; this test
// proves a real apiserver actually accepts the value, not just the
// fake.Client which skips enum validation.
func TestEnvtest_StatusPhaseLeaseBlockedAccepted(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-leaseblocked")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cr := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: "envtest-leaseblocked", Name: "edge-01"},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "edge-01"},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies: []string{"vlan"},
				Source: configv1alpha1.ConfigurationSource{
					Inline: &runtime.RawExtension{Raw: []byte("{}")},
				},
			},
		},
	}
	if err := c.Create(ctx, cr); err != nil {
		t.Fatalf("create CR: %v", err)
	}

	// Headline assertion: status.phase=LeaseBlocked must round-trip
	// through the real apiserver. Pre-Wave-9.1 this would fail because
	// the kubebuilder enum did not list LeaseBlocked.
	cr.Status.Phase = engine.PhaseLeaseBlocked
	if err := c.Status().Update(ctx, cr); err != nil {
		t.Fatalf("status update to LeaseBlocked rejected by apiserver: %v", err)
	}

	var got configv1alpha1.IOSXEConfig
	if err := c.Get(ctx,
		types.NamespacedName{Namespace: "envtest-leaseblocked", Name: "edge-01"},
		&got); err != nil {
		t.Fatalf("re-fetch CR: %v", err)
	}
	if got.Status.Phase != engine.PhaseLeaseBlocked {
		t.Errorf("phase did not round-trip: got %q, want %q",
			got.Status.Phase, engine.PhaseLeaseBlocked)
	}
}

// TestEnvtest_StatusPhaseEnumRejectsBogusValue is the negative control
// for the LeaseBlocked admission test. Without this, the previous test
// could pass even if the apiserver were silently ignoring the enum;
// asserting a bogus value is REJECTED proves the enum is being
// enforced (not just present in the schema).
func TestEnvtest_StatusPhaseEnumRejectsBogusValue(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-bogus-phase")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cr := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: "envtest-bogus-phase", Name: "edge-01"},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "edge-01"},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies: []string{"vlan"},
				Source: configv1alpha1.ConfigurationSource{
					Inline: &runtime.RawExtension{Raw: []byte("{}")},
				},
			},
		},
	}
	if err := c.Create(ctx, cr); err != nil {
		t.Fatalf("create CR: %v", err)
	}

	cr.Status.Phase = "DefinitelyNotAPhase"
	err := c.Status().Update(ctx, cr)
	if err == nil {
		t.Fatalf("apiserver accepted bogus phase %q — enum is not being enforced",
			cr.Status.Phase)
	}
	// Sanity: the error should mention the enum / the field. We don't
	// pin the exact wording (apiserver-version-dependent) but we do
	// pin that the failure was a validation rejection, not a network
	// error or a 404.
	if !strings.Contains(err.Error(), "phase") && !strings.Contains(err.Error(), "Unsupported") {
		t.Errorf("unexpected error shape: %v", err)
	}
}

// TestEnvtest_LeaseCreationForUnderscoreFamily is the W7R-1 smoke.
// Wave 8.1 added DNS-1123 sanitisation + SHA-256 hashing to leaseName;
// this test proves a real apiserver actually accepts the resulting
// Lease for an underscore-bearing family like interface_ethernet.
// Pre-Wave-8.1 this would fail with a name-validation error from the
// apiserver — fake.Client skipped that check.
func TestEnvtest_LeaseCreationForUnderscoreFamily(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-leasename")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	leaser := &engine.FamilyLeaser{
		Client:    c,
		Namespace: "envtest-leasename",
		TTL:       30 * time.Second,
	}
	res, err := leaser.Acquire(ctx, "edge-01", "interface_ethernet", "envtest-runner#abc")
	if err != nil {
		t.Fatalf("Acquire(interface_ethernet) rejected by apiserver: %v", err)
	}
	if !res.Owned {
		t.Errorf("expected newly-created lease to be owned, got %+v", res)
	}

	var leases coordv1.LeaseList
	if err := c.List(ctx, &leases, client.InNamespace("envtest-leasename")); err != nil {
		t.Fatalf("list leases: %v", err)
	}
	if len(leases.Items) != 1 {
		t.Fatalf("expected 1 lease, got %d", len(leases.Items))
	}
	name := leases.Items[0].Name
	if strings.Contains(name, "_") {
		t.Errorf("lease name %q still contains underscore — sanitisation regressed", name)
	}
	// Belt-and-suspenders: confirm the apiserver-stored name is shaped
	// like the leaseName composition (cvk-<device>-<family>-<hash>),
	// without pinning exact bytes (the hash suffix derives from the
	// raw inputs and changes if leaseName changes).
	if !strings.HasPrefix(name, "cvk-edge-01-") {
		t.Errorf("lease name %q lost the cvk-<device>-<family> prefix", name)
	}
}

// ─── Wave 10.2 + 10.3 — confirmTimeoutSeconds + atomicReplace admission ──
//
// Three tests pinning the kubebuilder-generated CRD validation for
// the new spec fields. Equivalent to the existing LeaseBlocked enum
// admission tests above but for the Wave 10 CRD additions.

// TestEnvtest_ConfirmTimeoutSecondsAdmittedByApiserver pins that
// the new spec.confirmTimeoutSeconds field round-trips a valid
// value (within the kubebuilder Min=0/Max=300 range) through a
// real apiserver.
func TestEnvtest_ConfirmTimeoutSecondsAdmittedByApiserver(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-cct")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cr := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: "envtest-cct", Name: "edge-01"},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "edge-01"},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies:       []string{"vlan"},
				Transactional:         true,
				ConfirmTimeoutSeconds: 30,
				Source: configv1alpha1.ConfigurationSource{
					Inline: &runtime.RawExtension{Raw: []byte("{}")},
				},
			},
		},
	}
	if err := c.Create(ctx, cr); err != nil {
		t.Fatalf("create CR with confirmTimeoutSeconds=30: %v", err)
	}
	var got configv1alpha1.IOSXEConfig
	if err := c.Get(ctx,
		types.NamespacedName{Namespace: "envtest-cct", Name: "edge-01"}, &got); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if got.Spec.ConfirmTimeoutSeconds != 30 {
		t.Errorf("confirmTimeoutSeconds did not round-trip: got %d, want 30", got.Spec.ConfirmTimeoutSeconds)
	}
}

// TestEnvtest_ConfirmTimeoutSecondsMaximumEnforced is the negative
// control for the max=300 kubebuilder bound. Setting 301 should be
// rejected by apiserver admission.
func TestEnvtest_ConfirmTimeoutSecondsMaximumEnforced(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-cct-max")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cr := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: "envtest-cct-max", Name: "edge-01"},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "edge-01"},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies:       []string{"vlan"},
				Transactional:         true,
				ConfirmTimeoutSeconds: 301, // out of bounds — kubebuilder Maximum=300
				Source: configv1alpha1.ConfigurationSource{
					Inline: &runtime.RawExtension{Raw: []byte("{}")},
				},
			},
		},
	}
	err := c.Create(ctx, cr)
	if err == nil {
		t.Fatalf("apiserver accepted confirmTimeoutSeconds=301 — Maximum=300 not enforced")
	}
	if !strings.Contains(err.Error(), "confirmTimeoutSeconds") &&
		!strings.Contains(err.Error(), "should be less than or equal to 300") {
		t.Errorf("unexpected error shape: %v", err)
	}
}

// TestEnvtest_AtomicReplaceFieldAdmitted pins that the new
// spec.atomicReplace boolean round-trips through the apiserver.
// Cheap admission sanity: kubebuilder defaults to false; the test
// sets true explicitly and asserts the value persists.
func TestEnvtest_AtomicReplaceFieldAdmitted(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-atomic")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cr := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: "envtest-atomic", Name: "edge-01"},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "edge-01"},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies: []string{"vlan"},
				Transactional:   true,
				AtomicReplace:   true,
				Source: configv1alpha1.ConfigurationSource{
					Inline: &runtime.RawExtension{Raw: []byte("{}")},
				},
			},
		},
	}
	if err := c.Create(ctx, cr); err != nil {
		t.Fatalf("create CR with atomicReplace=true: %v", err)
	}
	var got configv1alpha1.IOSXEConfig
	if err := c.Get(ctx,
		types.NamespacedName{Namespace: "envtest-atomic", Name: "edge-01"}, &got); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if !got.Spec.AtomicReplace {
		t.Errorf("atomicReplace did not round-trip: got false, want true")
	}
}

// ─── Wave 10 variation matrix — option C playbook coverage ──────────
//
// Five additional admission cases that pin the corners of the
// kubebuilder validation surface for the new fields. Complement the
// happy-path tests above without re-running the engine logic
// (which is unit-tested in transactional_test.go).

// TestEnvtest_ConfirmTimeoutSecondsBoundaryValues pins both ends
// of the kubebuilder Min=0/Max=300 constraint with admission tests.
// Min=0 (default-equivalent) and Max=300 (upper boundary) MUST be
// accepted; -1 and 301 MUST be rejected.
func TestEnvtest_ConfirmTimeoutSecondsBoundaryValues(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-cct-bounds")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	mk := func(name string, secs int32) *configv1alpha1.IOSXEConfig {
		return &configv1alpha1.IOSXEConfig{
			ObjectMeta: metav1.ObjectMeta{Namespace: "envtest-cct-bounds", Name: name},
			Spec: configv1alpha1.IOSXEConfigSpec{
				DeviceRef: configv1alpha1.DeviceRef{Name: "edge-01"},
				IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
					ManagedFamilies:       []string{"vlan"},
					Transactional:         true,
					ConfirmTimeoutSeconds: secs,
					Source: configv1alpha1.ConfigurationSource{
						Inline: &runtime.RawExtension{Raw: []byte("{}")},
					},
				},
			},
		}
	}

	// Min boundary: 0 must be accepted.
	if err := c.Create(ctx, mk("at-zero", 0)); err != nil {
		t.Errorf("confirmTimeoutSeconds=0 rejected: %v", err)
	}
	// Max boundary: 300 must be accepted.
	if err := c.Create(ctx, mk("at-max", 300)); err != nil {
		t.Errorf("confirmTimeoutSeconds=300 rejected: %v", err)
	}
	// Negative: -1 must be rejected (Min=0).
	if err := c.Create(ctx, mk("below-min", -1)); err == nil {
		t.Errorf("confirmTimeoutSeconds=-1 was accepted; Min=0 not enforced")
	}
	// Note: the >300 case is already covered by
	// TestEnvtest_ConfirmTimeoutSecondsMaximumEnforced above.
}

// TestEnvtest_AtomicReplaceWithConfirmedCommitCombined is the
// admission counterpart to live test 13 — proves the apiserver
// accepts a CR that opts in to BOTH safety nets. There is no
// kubebuilder rule that forbids this combination (and there
// shouldn't be — the user explicitly framed Wave 10 as a
// combined primitive), but a future schema change could
// accidentally introduce one. The test pins that the combination
// stays admissible.
func TestEnvtest_AtomicReplaceWithConfirmedCommitCombined(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-w10-combined")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cr := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: "envtest-w10-combined", Name: "edge-01"},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "edge-01"},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies:       []string{"vlan", "vrf", "interface_loopback"},
				Transactional:         true,
				AtomicReplace:         true,
				ConfirmTimeoutSeconds: 30,
				Source: configv1alpha1.ConfigurationSource{
					Inline: &runtime.RawExtension{Raw: []byte("{}")},
				},
			},
		},
	}
	if err := c.Create(ctx, cr); err != nil {
		t.Fatalf("CR with both safety nets rejected: %v", err)
	}
	var got configv1alpha1.IOSXEConfig
	if err := c.Get(ctx,
		types.NamespacedName{Namespace: "envtest-w10-combined", Name: "edge-01"}, &got); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if got.Spec.ConfirmTimeoutSeconds != 30 {
		t.Errorf("confirmTimeoutSeconds did not round-trip: %d", got.Spec.ConfirmTimeoutSeconds)
	}
	if !got.Spec.AtomicReplace {
		t.Errorf("atomicReplace did not round-trip: got false")
	}
}

// TestEnvtest_NonTransactionalCRWithConfirmTimeoutAdmissible
// pins the admission case for live test 11: a CR that opts in to
// confirmed-commit BUT sets transactional=false (the RESTCONF
// fallback shape) MUST be admissible. The "you can't have
// confirmTimeoutSeconds without transactional" check happens in
// the engine at runtime (it surfaces via Result.ConfirmedCommit
// Fallback), not at admission. A future kubebuilder CEL rule
// that forbids this combination would break the documented
// fallback contract; this test catches that regression.
func TestEnvtest_NonTransactionalCRWithConfirmTimeoutAdmissible(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-non-tx-cct")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cr := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: "envtest-non-tx-cct", Name: "edge-01"},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "edge-01"},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies:       []string{"vlan"},
				Transactional:         false, // RESTCONF shape
				ConfirmTimeoutSeconds: 30,    // operator-error case the engine surfaces as fallback
				Source: configv1alpha1.ConfigurationSource{
					Inline: &runtime.RawExtension{Raw: []byte("{}")},
				},
			},
		},
	}
	if err := c.Create(ctx, cr); err != nil {
		t.Fatalf("non-transactional + confirmTimeoutSeconds CR rejected by apiserver: %v (this combination must be admissible — the engine surfaces it via Result.ConfirmedCommitFallback)", err)
	}
}
