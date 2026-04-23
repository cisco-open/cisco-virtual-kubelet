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

package engine

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/intent"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers"
)

// --- stubs -----------------------------------------------------------------

// stubTransport is the smallest transport.Interface that satisfies the
// engine's calls. Counts fetches and mutates for assertions.
type stubTransport struct {
	caps     transport.Capabilities
	fetched  int
	mutated  int
	mutateFn func(tx transport.TxHandle, ops []transport.Op) error
}

func (s *stubTransport) Capabilities() transport.Capabilities { return s.caps }
func (s *stubTransport) Fetch(context.Context, string) ([]byte, error) {
	s.fetched++
	return nil, nil
}
func (s *stubTransport) StartTransaction(context.Context) (transport.TxHandle, error) {
	return "", nil
}
func (s *stubTransport) Mutate(_ context.Context, tx transport.TxHandle, ops []transport.Op) error {
	s.mutated++
	if s.mutateFn != nil {
		return s.mutateFn(tx, ops)
	}
	return nil
}
func (s *stubTransport) Commit(context.Context, transport.TxHandle) error  { return nil }
func (s *stubTransport) Discard(context.Context, transport.TxHandle) error { return nil }
func (s *stubTransport) SaveStartup(context.Context) error                 { return nil }
func (s *stubTransport) Close() error                                      { return nil }

type fakeWriter struct {
	family    string
	fetchErr  error
	diffErr   error
	applyErr  error
	ops       []transport.Op
	residual  []transport.Op
	fetches   int
	applies   int
	diffCalls int
}

func (w *fakeWriter) Family() string      { return w.family }
func (w *fakeWriter) YANGPaths() []string { return []string{"/" + w.family} }
func (w *fakeWriter) Fetch(context.Context, transport.Interface) (any, error) {
	w.fetches++
	return nil, w.fetchErr
}
func (w *fakeWriter) Diff(desired, observed any) ([]transport.Op, error) {
	w.diffCalls++
	if w.diffErr != nil {
		return nil, w.diffErr
	}
	// Second Diff call (verify) returns residual.
	if w.diffCalls > 1 {
		return w.residual, nil
	}
	return w.ops, nil
}
func (w *fakeWriter) Apply(context.Context, transport.Interface, []transport.Op) error {
	w.applies++
	return w.applyErr
}

func mkCR(name, device string, families ...string) *configv1alpha1.IOSXEConfig {
	return &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "network"},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: device},
			ManagedFamilies: families,
		},
	}
}

// --- tests -----------------------------------------------------------------

func TestReconcileInSyncWhenDiffEmpty(t *testing.T) {
	w := &fakeWriter{family: "vlan"} // no ops → InSync
	e := &Engine{
		Transport: &stubTransport{},
		Lookup:    func(f string) writers.SectionWriter { return w },
	}
	res := &intent.ResolvedIntent{
		DeviceName: "edge-01", ManagedFamilies: []string{"vlan"},
		Configuration: map[string]any{"vlan": map[string]any{}},
		DriftPolicy:   configv1alpha1.DriftPolicyRevert,
	}

	r := e.Reconcile(context.Background(), res)
	if r.Phase != PhaseInSync {
		t.Fatalf("Phase=%s, want InSync", r.Phase)
	}
	if w.applies != 0 {
		t.Fatalf("Apply called %d times on no-op", w.applies)
	}
}

func TestReconcileAppliesAndVerifies(t *testing.T) {
	w := &fakeWriter{
		family: "vlan",
		ops:    []transport.Op{{Verb: transport.VerbMerge, Path: "/x"}},
	}
	e := &Engine{
		Transport: &stubTransport{},
		Lookup:    func(f string) writers.SectionWriter { return w },
	}
	res := &intent.ResolvedIntent{
		DeviceName: "edge-01", ManagedFamilies: []string{"vlan"},
		Configuration: map[string]any{"vlan": map[string]any{}},
		DriftPolicy:   configv1alpha1.DriftPolicyRevert,
	}

	r := e.Reconcile(context.Background(), res)
	if r.Phase != PhaseInSync {
		t.Fatalf("Phase=%s, want InSync", r.Phase)
	}
	if w.applies != 1 {
		t.Fatalf("Apply called %d times, want 1", w.applies)
	}
	// Two Fetches: pre-plan and post-apply verify.
	if w.fetches != 2 {
		t.Fatalf("Fetch called %d times, want 2", w.fetches)
	}
	// Two Diffs: plan and verify.
	if w.diffCalls != 2 {
		t.Fatalf("Diff called %d times, want 2", w.diffCalls)
	}
}

func TestReconcileReportPolicyDoesNotApply(t *testing.T) {
	w := &fakeWriter{
		family: "vlan",
		ops:    []transport.Op{{Verb: transport.VerbMerge, Path: "/x"}},
	}
	e := &Engine{
		Transport: &stubTransport{},
		Lookup:    func(f string) writers.SectionWriter { return w },
	}
	res := &intent.ResolvedIntent{
		DeviceName: "edge-01", ManagedFamilies: []string{"vlan"},
		Configuration: map[string]any{"vlan": map[string]any{}},
		DriftPolicy:   configv1alpha1.DriftPolicyReport,
	}
	r := e.Reconcile(context.Background(), res)
	if r.Phase != PhaseDrifted {
		t.Fatalf("Phase=%s, want Drifted", r.Phase)
	}
	if w.applies != 0 {
		t.Fatal("Apply called under report policy")
	}
}

func TestReconcilePausePolicyReturnsEarly(t *testing.T) {
	w := &fakeWriter{family: "vlan", ops: []transport.Op{{Verb: transport.VerbMerge, Path: "/x"}}}
	e := &Engine{
		Transport: &stubTransport{},
		Lookup:    func(f string) writers.SectionWriter { return w },
	}
	res := &intent.ResolvedIntent{
		DeviceName: "edge-01", ManagedFamilies: []string{"vlan"},
		DriftPolicy: configv1alpha1.DriftPolicyPause,
	}
	r := e.Reconcile(context.Background(), res)
	if r.Phase != PhasePaused {
		t.Fatalf("Phase=%s, want Paused", r.Phase)
	}
	if w.fetches != 0 {
		t.Fatal("Fetch called under pause policy")
	}
}

func TestReconcileApplyErrorSurfacesOnStatus(t *testing.T) {
	w := &fakeWriter{
		family:   "vlan",
		ops:      []transport.Op{{Verb: transport.VerbMerge, Path: "/x"}},
		applyErr: errors.New("device 503"),
	}
	e := &Engine{
		Transport: &stubTransport{},
		Lookup:    func(f string) writers.SectionWriter { return w },
	}
	res := &intent.ResolvedIntent{
		DeviceName: "edge-01", ManagedFamilies: []string{"vlan"},
		DriftPolicy: configv1alpha1.DriftPolicyRevert,
	}
	r := e.Reconcile(context.Background(), res)
	if r.Phase != PhaseFailed {
		t.Fatalf("Phase=%s, want Failed", r.Phase)
	}
	if len(r.FamilyStatuses) != 1 || r.FamilyStatuses[0].State != "ApplyError" {
		t.Fatalf("FamilyStatuses=%#v", r.FamilyStatuses)
	}
}

func TestReconcileResidualDriftAfterRevertFails(t *testing.T) {
	// Apply succeeds but verify-diff is non-empty → Phase=Failed. This
	// is the "write was accepted but did not take effect" case.
	w := &fakeWriter{
		family:   "vlan",
		ops:      []transport.Op{{Verb: transport.VerbMerge, Path: "/a"}},
		residual: []transport.Op{{Verb: transport.VerbMerge, Path: "/a"}},
	}
	e := &Engine{
		Transport: &stubTransport{},
		Lookup:    func(f string) writers.SectionWriter { return w },
	}
	res := &intent.ResolvedIntent{
		DeviceName: "edge-01", ManagedFamilies: []string{"vlan"},
		DriftPolicy: configv1alpha1.DriftPolicyRevert,
	}
	r := e.Reconcile(context.Background(), res)
	if r.Phase != PhaseFailed {
		t.Fatalf("Phase=%s, want Failed", r.Phase)
	}
	if r.FamilyStatuses[0].State != "Drifted" {
		t.Fatalf("FamilyStatus=%#v, want Drifted", r.FamilyStatuses[0])
	}
}

func TestReconcileUnsupportedFamily(t *testing.T) {
	e := &Engine{
		Transport: &stubTransport{},
		Lookup:    func(f string) writers.SectionWriter { return nil },
	}
	res := &intent.ResolvedIntent{
		DeviceName: "edge-01", ManagedFamilies: []string{"not-a-family"},
		DriftPolicy: configv1alpha1.DriftPolicyRevert,
	}
	r := e.Reconcile(context.Background(), res)
	if r.Phase != PhaseFailed {
		t.Fatalf("Phase=%s, want Failed", r.Phase)
	}
	if r.FamilyStatuses[0].State != "Unsupported" {
		t.Fatalf("state=%q", r.FamilyStatuses[0].State)
	}
}

func TestConflictCheckReportsOverlap(t *testing.T) {
	crs := []*configv1alpha1.IOSXEConfig{
		mkCR("a", "edge-01", "vlan", "system"),
		mkCR("b", "edge-01", "vlan", "interface_ethernet"),
		mkCR("c", "other-device", "vlan"),
	}
	got := ConflictCheck("edge-01", crs)
	if _, ok := got["vlan"]; !ok {
		t.Fatalf("vlan conflict missing: got %#v", got)
	}
	if len(got["vlan"]) != 2 {
		t.Errorf("vlan owners=%v", got["vlan"])
	}
	if _, sys := got["system"]; sys {
		t.Errorf("system should not conflict (owned only by a)")
	}
	if _, ifs := got["interface_ethernet"]; ifs {
		t.Errorf("interface_ethernet should not conflict (owned only by b)")
	}
}

func TestReconcileNilIntent(t *testing.T) {
	e := &Engine{Transport: &stubTransport{}, Lookup: func(string) writers.SectionWriter { return nil }}
	r := e.Reconcile(context.Background(), nil)
	if r.Phase != PhaseFailed || r.Err == nil {
		t.Fatalf("nil intent should fail: %+v", r)
	}
}

func TestReconcileValidationRejectsEmptyTransport(t *testing.T) {
	e := &Engine{Lookup: func(string) writers.SectionWriter { return nil }}
	res := &intent.ResolvedIntent{
		DeviceName: "x", ManagedFamilies: []string{"vlan"},
	}
	r := e.Reconcile(context.Background(), res)
	if r.Phase != PhaseFailed {
		t.Fatalf("expected Failed; got %+v", r)
	}
}
