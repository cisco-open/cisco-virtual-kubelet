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
	"bytes"
	"context"
	"errors"
	"strings"
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
	family     string
	fetchErr   error
	diffErr    error
	applyErr   error
	ops        []transport.Op
	residual   []transport.Op
	appliedOps []transport.Op
	fetches    int
	applies    int
	diffCalls  int
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
func (w *fakeWriter) Apply(_ context.Context, _ transport.Interface, ops []transport.Op) error {
	w.applies++
	w.appliedOps = append(w.appliedOps, ops...)
	return w.applyErr
}

// fakePruneWriter wraps fakeWriter with PruneCapable behaviour. The
// engine's PruneOnRelinquish path is opt-in via this interface check,
// so the test fixture has to be a distinct type — fakeWriter without
// PruneDiff is the negative case.
type fakePruneWriter struct {
	*fakeWriter
	pruneOps     []transport.Op
	pruneErr     error
	pruneCalls   int
	pruneInputs  []any
	keysOfReturn func(any) []string
}

func (w *fakePruneWriter) PruneDiff(desired, observed any) ([]transport.Op, error) {
	w.pruneCalls++
	w.pruneInputs = append(w.pruneInputs, observed)
	if w.pruneErr != nil {
		return nil, w.pruneErr
	}
	return w.pruneOps, nil
}

// KeysOf is the KeyExtractable hook. Real writers extract list-key
// values from a desired/observed family blob. Tests that need
// substantive scoping behaviour stage `keysOfReturn`; otherwise the
// fake reports any []string passed verbatim.
func (w *fakePruneWriter) KeysOf(input any) []string {
	if w.keysOfReturn != nil {
		return w.keysOfReturn(input)
	}
	if ks, ok := input.([]string); ok {
		return ks
	}
	return nil
}

// fakePruneWriterNoKE is PruneCapable but NOT KeyExtractable —
// represents older / not-yet-uplifted writers. The F1 fix
// (2026-05-01) skips prune for these to avoid baseline wipes.
type fakePruneWriterNoKE struct {
	*fakeWriter
	pruneCalls int
}

func (w *fakePruneWriterNoKE) PruneDiff(desired, observed any) ([]transport.Op, error) {
	w.pruneCalls++
	return nil, nil
}

func mkCR(name, device string, families ...string) *configv1alpha1.IOSXEConfig {
	return &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "network"},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: device},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies: families,
			},
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

func TestReconcilePruneOnRelinquishCallsPruneDiff(t *testing.T) {
	// PruneOnRelinquish: true + writer implements PruneCapable ⇒ the
	// engine appends prune ops to the additive ones. Two Apply calls
	// overall — the additive op and the prune op land via the same
	// Mutate (Apply is called once, batching the ops).
	pw := &fakePruneWriter{
		fakeWriter: &fakeWriter{
			family: "vlan",
			ops:    []transport.Op{{Verb: transport.VerbMerge, Path: "/want"}},
		},
		pruneOps: []transport.Op{{Verb: transport.VerbDelete, Path: "/orphan"}},
	}
	e := &Engine{
		Transport: &stubTransport{},
		Lookup:    func(f string) writers.SectionWriter { return pw },
	}
	res := &intent.ResolvedIntent{
		DeviceName: "edge-01", ManagedFamilies: []string{"vlan"},
		Configuration:     map[string]any{"vlan": map[string]any{}},
		DriftPolicy:       configv1alpha1.DriftPolicyRevert,
		PruneOnRelinquish: true,
	}
	_ = e.Reconcile(context.Background(), res)
	if pw.pruneCalls < 1 {
		t.Fatalf("PruneDiff calls=%d, want at least 1", pw.pruneCalls)
	}
	// Verify the additive op precedes the prune op — engine must
	// concatenate Diff first, PruneDiff second.
	if len(pw.appliedOps) < 2 {
		t.Fatalf("got %d applied ops, want at least 2: %#v", len(pw.appliedOps), pw.appliedOps)
	}
	if pw.appliedOps[0].Verb != transport.VerbMerge || pw.appliedOps[1].Verb != transport.VerbDelete {
		t.Errorf("op order wrong: %#v", pw.appliedOps)
	}
}

func TestReconcilePruneOnRelinquishSkippedWhenWriterNotCapable(t *testing.T) {
	// fakeWriter does NOT implement PruneCapable. Engine must
	// silently skip the prune step rather than erroring — that's
	// how families roll out support one at a time.
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
		Configuration:     map[string]any{"vlan": map[string]any{}},
		DriftPolicy:       configv1alpha1.DriftPolicyRevert,
		PruneOnRelinquish: true,
	}
	r := e.Reconcile(context.Background(), res)
	if r.Phase != PhaseInSync {
		t.Fatalf("Phase=%s, want InSync (writer is not prune-capable, so flag is a no-op)", r.Phase)
	}
}

// TestReconcilePruneOnRelinquishWithoutAtomicReplaceIsScoped pins the
// F1 fix (2026-05-01): pruneOnRelinquish without atomicReplace must
// scope prune to ownedKeys ∪ desired, not the full observed set.
//
// Pre-fix, the AtomicReplace guard at engine.go:794 caused
// scope-to-owned to be skipped under pruneOnRelinquish=true /
// atomicReplace=false, so the next reconcile would feed the writer's
// PruneDiff a full observed list — and PruneDiff then deleted every
// observed entry not in desired, including baseline state the CR had
// never touched (live-validated wipe of pre-existing VLANs against
// cat9000-1 / 9300-4).
func TestReconcilePruneOnRelinquishWithoutAtomicReplaceIsScoped(t *testing.T) {
	const family = "vlan"
	// Writer reports observed keys as a fixed set including baseline
	// entries the CR has never touched (2, 3) plus the one it owns
	// (4001). Desired pins 4001 only. priorOwned = ["4001"].
	pw := &fakePruneWriter{
		fakeWriter: &fakeWriter{family: family},
		pruneOps:   []transport.Op{{Verb: transport.VerbDelete, Path: "/v"}},
		keysOfReturn: func(input any) []string {
			// observed → all keys; desired → pinned key
			if m, ok := input.(map[string]any); ok && len(m) == 0 {
				// empty desired family → no keys
				return nil
			}
			if input == nil {
				// engine passes nil for observed in this fake
				return []string{"2", "3", "4001"}
			}
			return []string{"4001"}
		},
	}
	e := &Engine{
		Transport: &stubTransport{},
		Lookup:    func(f string) writers.SectionWriter { return pw },
	}
	res := &intent.ResolvedIntent{
		DeviceName: "edge-01", ManagedFamilies: []string{family},
		// desired: vlan 4001 only.
		Configuration:     map[string]any{family: map[string]any{"4001": "owned"}},
		DriftPolicy:       configv1alpha1.DriftPolicyRevert,
		PruneOnRelinquish: true,
		AtomicReplace:     false, // <-- the F1 case
		AtomicReplaceOwnedKeys: map[string][]string{
			family: {"4001"},
		},
	}
	_ = e.Reconcile(context.Background(), res)
	if pw.pruneCalls < 1 {
		t.Fatalf("PruneDiff calls=%d, want at least 1 (pruneOnRelinquish path must still run)", pw.pruneCalls)
	}
	// The first PruneDiff input must be a scoped stub list, NOT the
	// raw observed (which would have keys 2, 3, 4001). With scoping,
	// only keys present in priorOwned ∪ desired (= {4001}) survive
	// the filter — and even then only if observed contains them.
	if len(pw.pruneInputs) == 0 {
		t.Fatalf("no PruneDiff inputs captured")
	}
	stubs, ok := pw.pruneInputs[0].([]map[string]any)
	if !ok {
		t.Fatalf("PruneDiff input not a stub list (scoping bypassed?): %T = %#v", pw.pruneInputs[0], pw.pruneInputs[0])
	}
	for _, stub := range stubs {
		// Disallow baseline keys 2 / 3 in the prune input.
		for _, kf := range []string{"name", "id", "sequence"} {
			if v, ok := stub[kf]; ok {
				if v == "2" || v == "3" {
					t.Errorf("scoped prune input leaked baseline key %v: %#v", v, stub)
				}
			}
		}
	}
}

// TestReconcilePruneOnRelinquishUnsupportedWhenNotKeyExtractable
// pins the A3 fix (2026-05-01): when a writer is PruneCapable but
// not KeyExtractable, pruneOnRelinquish must surface the family as
// Unsupported rather than silently no-op. Pre-fix, the engine
// skipped prune and reported InSync, hiding the fact that requested
// cleanup never ran.
func TestReconcilePruneOnRelinquishUnsupportedWhenNotKeyExtractable(t *testing.T) {
	w := &fakePruneWriterNoKE{
		fakeWriter: &fakeWriter{
			family: "vlan",
			ops:    []transport.Op{{Verb: transport.VerbMerge, Path: "/x"}},
		},
	}
	e := &Engine{
		Transport: &stubTransport{},
		Lookup:    func(f string) writers.SectionWriter { return w },
	}
	res := &intent.ResolvedIntent{
		DeviceName: "edge-01", ManagedFamilies: []string{"vlan"},
		Configuration:     map[string]any{"vlan": map[string]any{}},
		DriftPolicy:       configv1alpha1.DriftPolicyRevert,
		PruneOnRelinquish: true,
	}
	r := e.Reconcile(context.Background(), res)
	if w.pruneCalls != 0 {
		t.Fatalf("PruneDiff was called %d times on a non-KeyExtractable writer; "+
			"the engine must short-circuit before calling PruneDiff", w.pruneCalls)
	}
	if len(r.FamilyStatuses) != 1 || r.FamilyStatuses[0].State != "Unsupported" {
		t.Fatalf("family state = %#v, want Unsupported", r.FamilyStatuses)
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

// TestCLIBlocksAppliedAfterFamilies verifies the engine runs the
// family writers first, then pushes CLI blocks. One transport.Op
// with VerbCLI per block.
func TestCLIBlocksAppliedAfterFamilies(t *testing.T) {
	// fakeWriter for a single in-sync family so families land
	// clean; engine should proceed to CLI.
	w := &fakeWriter{family: "vlan"} // no ops → InSync

	var (
		cliCount  int
		cliBodies [][]byte
	)
	mock := &stubTransport{
		mutateFn: func(tx transport.TxHandle, ops []transport.Op) error {
			for _, op := range ops {
				if op.Verb == transport.VerbCLI {
					cliCount++
					cliBodies = append(cliBodies, append([]byte(nil), op.Body...))
				}
			}
			return nil
		},
	}

	e := &Engine{
		Transport: mock,
		Lookup:    func(string) writers.SectionWriter { return w },
	}
	res := &intent.ResolvedIntent{
		DeviceName:      "edge-01",
		ManagedFamilies: []string{"vlan"},
		Configuration:   map[string]any{"vlan": map[string]any{}},
		DriftPolicy:     configv1alpha1.DriftPolicyRevert,
		CLIBlocks: []intent.CLIBlock{
			{TemplateName: "hostname", CLI: "hostname edge-01"},
			{TemplateName: "banner", CLI: "banner motd\nCompany policy applies\n^"},
		},
	}

	result := e.Reconcile(context.Background(), res)
	if result.Phase != PhaseInSync {
		t.Fatalf("phase=%s, want InSync", result.Phase)
	}
	if cliCount != 2 {
		t.Fatalf("VerbCLI ops seen=%d, want 2", cliCount)
	}

	// Both bodies should round-trip verbatim — engine passes the
	// CLI text through without reshaping.
	wantBodies := []string{"hostname edge-01", "banner motd"}
	for _, want := range wantBodies {
		found := false
		for _, body := range cliBodies {
			if bytes.Contains(body, []byte(want)) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no CLI body contained %q; bodies=%q", want, cliBodies)
		}
	}

	// Status list should carry both netascode-family entry + CLI
	// entries, distinguishable by the "cli:" prefix.
	foundCLI := 0
	for _, fs := range result.FamilyStatuses {
		if strings.HasPrefix(fs.Name, "cli:") {
			foundCLI++
		}
	}
	if foundCLI != 2 {
		t.Errorf("CLI FamilyStatuses=%d, want 2", foundCLI)
	}
}

func TestCLIBlocksSkippedUnderReportPolicy(t *testing.T) {
	// Under driftPolicy=report CLI blocks must surface as drift,
	// not be applied — matching the read-only semantics applied
	// to family writers.
	w := &fakeWriter{family: "vlan"}
	var cliCount int
	mock := &stubTransport{
		mutateFn: func(tx transport.TxHandle, ops []transport.Op) error {
			for _, op := range ops {
				if op.Verb == transport.VerbCLI {
					cliCount++
				}
			}
			return nil
		},
	}
	e := &Engine{Transport: mock, Lookup: func(string) writers.SectionWriter { return w }}
	res := &intent.ResolvedIntent{
		DeviceName:      "edge-01",
		ManagedFamilies: []string{"vlan"},
		Configuration:   map[string]any{"vlan": map[string]any{}},
		DriftPolicy:     configv1alpha1.DriftPolicyReport,
		CLIBlocks:       []intent.CLIBlock{{TemplateName: "hostname", CLI: "hostname x"}},
	}

	result := e.Reconcile(context.Background(), res)
	if cliCount != 0 {
		t.Fatalf("CLI op ran under report policy (cliCount=%d)", cliCount)
	}
	if result.Phase != PhaseDrifted {
		t.Fatalf("phase=%s, want Drifted", result.Phase)
	}

	// Expect a drift entry + a Drifted FamilyStatus for the
	// CLI block.
	foundDrift := 0
	for _, d := range result.Drift {
		if strings.HasPrefix(d.Family, "cli:") {
			foundDrift++
		}
	}
	if foundDrift != 1 {
		t.Errorf("CLI drift entries=%d, want 1", foundDrift)
	}
}

func TestCLIBlocksSkippedWhenFamiliesFailed(t *testing.T) {
	// When a family-writer apply errors, CLI blocks should NOT
	// run — they typically depend on structural state the
	// family writer was supposed to create.
	w := &fakeWriter{
		family:   "vlan",
		ops:      []transport.Op{{Verb: transport.VerbMerge, Path: "/v"}},
		applyErr: errors.New("device 503"),
	}
	var cliCount int
	mock := &stubTransport{
		mutateFn: func(tx transport.TxHandle, ops []transport.Op) error {
			for _, op := range ops {
				if op.Verb == transport.VerbCLI {
					cliCount++
				}
			}
			return nil
		},
	}
	e := &Engine{Transport: mock, Lookup: func(string) writers.SectionWriter { return w }}
	res := &intent.ResolvedIntent{
		DeviceName:      "edge-01",
		ManagedFamilies: []string{"vlan"},
		DriftPolicy:     configv1alpha1.DriftPolicyRevert,
		CLIBlocks:       []intent.CLIBlock{{TemplateName: "hostname", CLI: "hostname x"}},
	}

	result := e.Reconcile(context.Background(), res)
	if result.Phase != PhaseFailed {
		t.Fatalf("phase=%s, want Failed", result.Phase)
	}
	if cliCount != 0 {
		t.Errorf("CLI op ran after family-apply failure (cliCount=%d)", cliCount)
	}
}

// ─── Wave 10.3 — atomic replace ─────────────────────────────────────

// TestReconcileAtomicReplaceImpliesPrune pins the engine-boundary
// coupling: AtomicReplace=true MUST drive the per-family
// PruneCapable.PruneDiff path even when the operator did not set
// PruneOnRelinquish=true explicitly. This is what makes atomic
// replace strictly stronger than pruneOnRelinquish — same
// per-family delete-set, plus the cross-family ordering applied
// elsewhere.
func TestReconcileAtomicReplaceImpliesPrune(t *testing.T) {
	pw := &fakePruneWriter{
		fakeWriter: &fakeWriter{
			family: "vlan",
			ops:    []transport.Op{{Verb: transport.VerbMerge, Path: "/intent"}},
		},
		pruneOps: []transport.Op{{Verb: transport.VerbDelete, Path: "/orphan"}},
	}
	e := &Engine{
		Transport: &stubTransport{},
		Lookup:    func(string) writers.SectionWriter { return pw },
	}
	res := &intent.ResolvedIntent{
		DeviceName:      "edge-01",
		ManagedFamilies: []string{"vlan"},
		Configuration:   map[string]any{"vlan": map[string]any{}},
		DriftPolicy:     configv1alpha1.DriftPolicyRevert,
		// Critical: AtomicReplace=true, PruneOnRelinquish=false. The
		// engine must derive prune behavior from AtomicReplace alone.
		AtomicReplace:     true,
		PruneOnRelinquish: false,
	}
	_ = e.Reconcile(context.Background(), res)
	if pw.pruneCalls < 1 {
		t.Fatalf("PruneDiff calls=%d, want >= 1 (AtomicReplace=true should imply prune behaviour)", pw.pruneCalls)
	}
}

// TestReconcileFamilyOrderHookIsApplied pins the cross-family
// ordering wiring. The engine MUST consult Engine.FamilyOrder
// (when non-nil) and process families in the order it returns —
// not the order in res.ManagedFamilies. iosxebuilder will populate
// this with a topo-sort over schema/families.yaml's depends_on,
// but the engine doesn't care about the source; it cares that the
// hook is honored.
func TestReconcileFamilyOrderHookIsApplied(t *testing.T) {
	var seen []string
	w := func(family string) writers.SectionWriter {
		return &fakeWriter{
			family: family,
			// Record processing order via a closure on `seen`.
			// fakeWriter doesn't expose a hook, so use a wrapper
			// that captures family on Apply.
			ops: []transport.Op{{Verb: transport.VerbMerge, Path: "/" + family}},
		}
	}
	// Wrap Lookup to record the order it's called in. The engine
	// calls Lookup once per family per tick; recording on Lookup
	// gives us the exact iteration order.
	e := &Engine{
		Transport: &stubTransport{},
		Lookup: func(family string) writers.SectionWriter {
			seen = append(seen, family)
			return w(family)
		},
		// Reverse the operator-given order — exposes whether the
		// hook is applied.
		FamilyOrder: func(in []string) []string {
			out := make([]string, len(in))
			for i, f := range in {
				out[len(in)-1-i] = f
			}
			return out
		},
	}
	res := &intent.ResolvedIntent{
		DeviceName:      "edge-01",
		ManagedFamilies: []string{"a", "b", "c"},
		Configuration:   map[string]any{"a": map[string]any{}, "b": map[string]any{}, "c": map[string]any{}},
		DriftPolicy:     configv1alpha1.DriftPolicyRevert,
	}
	_ = e.Reconcile(context.Background(), res)
	// Expect reverse-of-input order: c, b, a.
	if len(seen) != 3 || seen[0] != "c" || seen[1] != "b" || seen[2] != "a" {
		t.Fatalf("family iteration order = %v, want [c b a]", seen)
	}
}

// TestReconcileFamilyOrderHookNilPreservesInputOrder is the
// backward-compat regression for the new Engine.FamilyOrder field.
// Existing engines that don't set the hook MUST process families
// in res.ManagedFamilies order — same as the pre-Wave-10
// behaviour.
func TestReconcileFamilyOrderHookNilPreservesInputOrder(t *testing.T) {
	var seen []string
	e := &Engine{
		Transport: &stubTransport{},
		Lookup: func(family string) writers.SectionWriter {
			seen = append(seen, family)
			return &fakeWriter{
				family: family,
				ops:    []transport.Op{{Verb: transport.VerbMerge, Path: "/" + family}},
			}
		},
		// FamilyOrder is nil — engine must use res.ManagedFamilies as-is.
	}
	res := &intent.ResolvedIntent{
		DeviceName:      "edge-01",
		ManagedFamilies: []string{"a", "b", "c"},
		Configuration:   map[string]any{"a": map[string]any{}, "b": map[string]any{}, "c": map[string]any{}},
		DriftPolicy:     configv1alpha1.DriftPolicyRevert,
	}
	_ = e.Reconcile(context.Background(), res)
	if len(seen) != 3 || seen[0] != "a" || seen[1] != "b" || seen[2] != "c" {
		t.Fatalf("family iteration order = %v, want [a b c] (nil FamilyOrder = input order)", seen)
	}
}
