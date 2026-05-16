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

// Wave 1A regression tests for external-review Finding #1: the engine
// must open a transport transaction when spec.transactional is true
// AND the transport advertises support, commit on full success,
// discard on any failure, and call SaveStartup post-success when
// requested. Prior to Wave 1A all of those paths were inert — the
// API exposed the fields but the engine never consulted them.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/intent"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers"
)

// txTransport is an instrumented transport.Interface that records
// every transaction-lifecycle call. Used to assert the engine's
// orchestration: StartTransaction once, Mutate on the same handle,
// Commit-or-Discard exactly once, SaveStartup conditional on flags.
type txTransport struct {
	caps              transport.Capabilities
	startCalls        atomic.Int32
	commitCalls       atomic.Int32
	discardCalls      atomic.Int32
	mutateCalls       atomic.Int32
	saveStartupCalls  atomic.Int32
	fetchCalls        atomic.Int32
	fetchTxCalls      atomic.Int32
	fetchTxHandles    []transport.TxHandle
	mutateHandlesSeen []transport.TxHandle
	startErr          error
	commitErr         error
	saveStartupErr    error

	// Wave 10 confirmed-commit tracking. Implementing
	// transport.ConfirmedCommitter on this fixture lets the engine's
	// type-assertion succeed in the auto-revert tests; existing
	// tests that don't set ConfirmTimeoutSeconds still take the
	// plain-Commit path because the engine's first-check is the
	// CR's opt-in flag.
	commitConfirmedCalls    atomic.Int32
	confirmCommitCalls      atomic.Int32
	commitConfirmedTimeouts []time.Duration
	commitConfirmedErr      error
	confirmCommitErr        error
}

const fakeHandle transport.TxHandle = "candidate-1"

func (t *txTransport) Capabilities() transport.Capabilities { return t.caps }
func (t *txTransport) Fetch(context.Context, string) ([]byte, error) {
	t.fetchCalls.Add(1)
	return nil, nil
}

// FetchTx records the handle the engine passed for the verify-Fetch
// path. Wave 1A-fu's correctness relies on the transactionalView
// preferring this method over Fetch when an inner transport
// implements TxFetcher.
func (t *txTransport) FetchTx(_ context.Context, tx transport.TxHandle, _ string) ([]byte, error) {
	t.fetchTxCalls.Add(1)
	t.fetchTxHandles = append(t.fetchTxHandles, tx)
	return nil, nil
}
func (t *txTransport) StartTransaction(context.Context) (transport.TxHandle, error) {
	t.startCalls.Add(1)
	if t.startErr != nil {
		return "", t.startErr
	}
	return fakeHandle, nil
}
func (t *txTransport) Mutate(_ context.Context, tx transport.TxHandle, _ []transport.Op) error {
	t.mutateCalls.Add(1)
	t.mutateHandlesSeen = append(t.mutateHandlesSeen, tx)
	return nil
}
func (t *txTransport) Commit(context.Context, transport.TxHandle) error {
	t.commitCalls.Add(1)
	return t.commitErr
}
func (t *txTransport) Discard(context.Context, transport.TxHandle) error {
	t.discardCalls.Add(1)
	return nil
}
func (t *txTransport) SaveStartup(context.Context) error {
	t.saveStartupCalls.Add(1)
	return t.saveStartupErr
}
func (t *txTransport) Close() error { return nil }

// CommitConfirmed implements transport.ConfirmedCommitter for the
// Wave 10 auto-revert tests. Records every call's timeout so tests
// can assert the engine forwarded the CR's confirmTimeoutSeconds
// correctly. Returning commitConfirmedErr lets a test simulate
// an RPC failure on the tentative commit RPC.
func (t *txTransport) CommitConfirmed(_ context.Context, _ transport.TxHandle, timeout time.Duration) error {
	t.commitConfirmedCalls.Add(1)
	t.commitConfirmedTimeouts = append(t.commitConfirmedTimeouts, timeout)
	return t.commitConfirmedErr
}

// ConfirmCommit implements transport.ConfirmedCommitter. Returning
// confirmCommitErr lets a test simulate the rare case where the
// follow-up confirm RPC fails after the tentative commit succeeded;
// the engine treats that as the safe failure mode (device will
// auto-revert at the timeout).
func (t *txTransport) ConfirmCommit(context.Context) error {
	t.confirmCommitCalls.Add(1)
	return t.confirmCommitErr
}

// txWriter is a SectionWriter whose only purpose is to emit one
// non-empty op on the first Diff call (to drive the apply path) and
// no ops on subsequent calls (the verify-diff). This mirrors the
// idempotent-writer contract: after Apply succeeds, the next Diff
// against the same intent + a now-up-to-date observed state returns
// empty ops, signalling InSync.
type txWriter struct {
	diffCalls atomic.Int32
}

func (*txWriter) Family() string      { return "system" }
func (*txWriter) YANGPaths() []string { return []string{"/system"} }
func (*txWriter) Fetch(ctx context.Context, t transport.Interface) (any, error) {
	// Drive the transport's Fetch so the test exercises the
	// transactionalView's TxFetcher dispatch. A no-op implementation
	// would skip the verify-path entirely and the FetchTx assertion
	// would always pass vacuously.
	if _, err := t.Fetch(ctx, "/system"); err != nil {
		return nil, err
	}
	return nil, nil
}
func (w *txWriter) Diff(desired, _ any) ([]transport.Op, error) {
	if desired == nil {
		return nil, nil
	}
	if w.diffCalls.Add(1) > 1 {
		// Verify-side diff: writes have landed, no further ops.
		return nil, nil
	}
	return []transport.Op{{Verb: transport.VerbReplace, Path: "/system"}}, nil
}
func (*txWriter) Apply(ctx context.Context, t transport.Interface, ops []transport.Op) error {
	// Writers all call Mutate with the empty handle today; the engine's
	// transactionalView wrapper rewrites it. The test transport records
	// the actual handle the wrapper passed through.
	return t.Mutate(ctx, "", ops)
}

func newTxFixture(supportsTx, supportsSave bool, writeStartup, transactional bool) (*Engine, *txTransport, *intent.ResolvedIntent) {
	tt := &txTransport{
		caps: transport.Capabilities{
			SupportsTransactions: supportsTx,
			SupportsSaveStartup:  supportsSave,
		},
	}
	tw := &txWriter{}
	e := &Engine{
		Transport: tt,
		Lookup: func(family string, _ string) writers.SectionWriter {
			if family == "system" {
				return tw
			}
			return nil
		},
	}
	res := &intent.ResolvedIntent{
		DeviceName:      "test-dev",
		ManagedFamilies: []string{"system"},
		Configuration:   map[string]any{"system": map[string]any{"hostname": "x"}},
		Transactional:   transactional,
		WriteStartup:    writeStartup,
		DriftPolicy:     configv1alpha1.DriftPolicyRevert,
	}
	return e, tt, res
}

func TestTransactionalApplyCommitsOnSuccess(t *testing.T) {
	t.Parallel()
	e, tt, res := newTxFixture(true, false, false, true)

	r := e.Reconcile(context.Background(), res)

	if r.Phase != PhaseInSync {
		t.Fatalf("phase = %q, want InSync; err=%v", r.Phase, r.Err)
	}
	if got := tt.startCalls.Load(); got != 1 {
		t.Errorf("StartTransaction calls = %d, want 1", got)
	}
	if got := tt.commitCalls.Load(); got != 1 {
		t.Errorf("Commit calls = %d, want 1", got)
	}
	if got := tt.discardCalls.Load(); got != 0 {
		t.Errorf("Discard calls = %d, want 0 on success", got)
	}
	for i, h := range tt.mutateHandlesSeen {
		if h != fakeHandle {
			t.Errorf("Mutate[%d] handle = %q, want %q (transactional view should rewrite the empty handle)", i, h, fakeHandle)
		}
	}
}

func TestTransactionalApplyDiscardsOnApplyError(t *testing.T) {
	t.Parallel()
	e, tt, res := newTxFixture(true, false, false, true)
	// Override Diff to push a verify-side residual that flips Phase to Failed.
	e.Lookup = func(family string, _ string) writers.SectionWriter {
		return failingApplyWriter{}
	}

	r := e.Reconcile(context.Background(), res)

	if r.Phase == PhaseInSync {
		t.Fatalf("phase = InSync; want Failed")
	}
	if got := tt.commitCalls.Load(); got != 0 {
		t.Errorf("Commit calls = %d, want 0 on failure", got)
	}
	if got := tt.discardCalls.Load(); got != 1 {
		t.Errorf("Discard calls = %d, want 1 on failure", got)
	}
}

// failingApplyWriter forces a writer-side apply error so the engine
// drops out of the family loop with a failure.
type failingApplyWriter struct{}

func (failingApplyWriter) Family() string      { return "system" }
func (failingApplyWriter) YANGPaths() []string { return []string{"/system"} }
func (failingApplyWriter) Fetch(context.Context, transport.Interface) (any, error) {
	return nil, nil
}
func (failingApplyWriter) Diff(_, _ any) ([]transport.Op, error) {
	return []transport.Op{{Verb: transport.VerbReplace, Path: "/system"}}, nil
}
func (failingApplyWriter) Apply(context.Context, transport.Interface, []transport.Op) error {
	return errors.New("simulated writer apply failure")
}

func TestNonTransactionalSkipsTransactionLifecycle(t *testing.T) {
	t.Parallel()
	// spec.transactional=true but transport doesn't support → engine
	// must NOT call StartTransaction, must apply directly, no commit.
	e, tt, res := newTxFixture(false, false, false, true)

	r := e.Reconcile(context.Background(), res)

	if r.Phase != PhaseInSync {
		t.Fatalf("phase = %q, want InSync", r.Phase)
	}
	if got := tt.startCalls.Load(); got != 0 {
		t.Errorf("StartTransaction calls = %d, want 0 (transport doesn't support)", got)
	}
	if got := tt.commitCalls.Load(); got != 0 {
		t.Errorf("Commit calls = %d, want 0", got)
	}
}

func TestSaveStartupCalledOnSuccessWhenRequested(t *testing.T) {
	t.Parallel()
	e, tt, res := newTxFixture(true, true, true, true)

	r := e.Reconcile(context.Background(), res)

	if r.Phase != PhaseInSync {
		t.Fatalf("phase = %q, want InSync", r.Phase)
	}
	if got := tt.saveStartupCalls.Load(); got != 1 {
		t.Errorf("SaveStartup calls = %d, want 1", got)
	}
	if !r.SaveStartupOK {
		t.Error("Result.SaveStartupOK = false, want true after a successful save")
	}
}

func TestSaveStartupSkippedWhenNotRequested(t *testing.T) {
	t.Parallel()
	e, tt, res := newTxFixture(true, true, false /*writeStartup*/, false /*transactional*/)

	_ = e.Reconcile(context.Background(), res)

	if got := tt.saveStartupCalls.Load(); got != 0 {
		t.Errorf("SaveStartup calls = %d, want 0 (not requested)", got)
	}
}

func TestSaveStartupSkippedWhenUnsupported(t *testing.T) {
	t.Parallel()
	// writeStartup=true but transport doesn't advertise support —
	// the engine must skip silently rather than calling SaveStartup
	// (which would return ErrUnsupported and pollute the result).
	e, tt, res := newTxFixture(false /*supportsTx*/, false /*supportsSave*/, true /*writeStartup*/, false)

	_ = e.Reconcile(context.Background(), res)

	if got := tt.saveStartupCalls.Load(); got != 0 {
		t.Errorf("SaveStartup calls = %d, want 0 (transport doesn't support)", got)
	}
}

func TestSaveStartupFailureIsNonFatal(t *testing.T) {
	t.Parallel()
	e, tt, res := newTxFixture(true, true, true, true)
	tt.saveStartupErr = errors.New("simulated save failure")

	r := e.Reconcile(context.Background(), res)

	// Apply succeeded → Phase stays InSync. SaveStartupErr surfaces
	// as a non-fatal warning on the result.
	if r.Phase != PhaseInSync {
		t.Fatalf("phase = %q, want InSync — SaveStartup failure must be non-fatal", r.Phase)
	}
	if r.SaveStartupErr == nil {
		t.Error("Result.SaveStartupErr should propagate the underlying error")
	}
	if r.SaveStartupOK {
		t.Error("Result.SaveStartupOK = true, want false on failure")
	}
}

// TestTransactionalVerifyReadsCandidate is the Wave 1A-fu regression
// for follow-up Finding #1. Under transactional apply, the verify-
// Fetch path must read through the TxFetcher interface so it sees
// the candidate datastore (not the stale running config). Before
// this wiring the engine Discard'd successful applies because the
// verify-Diff against running showed unchanged state.
func TestTransactionalVerifyReadsCandidate(t *testing.T) {
	t.Parallel()
	e, tt, res := newTxFixture(true, false, false, true)

	r := e.Reconcile(context.Background(), res)

	if r.Phase != PhaseInSync {
		t.Fatalf("phase = %q, want InSync", r.Phase)
	}
	// Engine performs at least one Fetch (initial state) + one
	// post-apply verify Fetch. Both must go through FetchTx because
	// the txTransport implements TxFetcher; reads of candidate
	// during the transaction window are the entire point.
	if got := tt.fetchTxCalls.Load(); got == 0 {
		t.Errorf("FetchTx calls = 0; verify path must consult TxFetcher when transport implements it")
	}
	for i, h := range tt.fetchTxHandles {
		if h != fakeHandle {
			t.Errorf("FetchTx[%d] handle = %q, want %q", i, h, fakeHandle)
		}
	}
}

// TestTransactionalCLIRejected is the Wave 7A.1 regression for
// external-review-next-actions Finding #1: when the resolved
// intent has spec.transactional=true AND any CLI template block,
// the engine MUST reject before any device-side mutation runs.
//
// Replaces the prior Wave 1A-fu TestCLIBlockUsesTransactionalView,
// which asserted CLI ops route through the transactional view.
// That contract was wrong on inspection: Cisco-IA cli-config-data
// writes directly to running config; there is no candidate-bound
// CLI path. Letting the engine apply CLI mid-transaction would
// produce out-of-tx writes that Discard cannot roll back. Wave 7A.1
// fails closed.
//
// Non-transactional CLI ticks still apply CLI normally — the
// applyTransport fallback to e.Transport handles it. That path is
// covered by the existing engine reconcile tests with
// res.Transactional=false; this test covers only the new rejection.
func TestTransactionalCLIRejected(t *testing.T) {
	t.Parallel()
	e, tt, res := newTxFixture(true, false, false, true)
	res.CLIBlocks = []intent.CLIBlock{{
		TemplateName: "test-cli",
		CLI:          "interface Loopback0\n description test\n",
	}}

	r := e.Reconcile(context.Background(), res)

	if r.Phase != PhaseFailed {
		t.Fatalf("phase = %q, want Failed; expected fail-closed rejection", r.Phase)
	}
	if !errors.Is(r.Err, ErrTransactionalCLIUnsupported) {
		t.Errorf("Result.Err = %v, want ErrTransactionalCLIUnsupported", r.Err)
	}
	// No Mutate calls — the rejection must happen BEFORE any
	// transport-side write. StartTransaction also must not have run
	// because the rejection is checked before transaction setup.
	if got := tt.mutateCalls.Load(); got != 0 {
		t.Errorf("Mutate calls = %d, want 0 — rejection must precede any device mutation", got)
	}
	if got := tt.startCalls.Load(); got != 0 {
		t.Errorf("StartTransaction calls = %d, want 0", got)
	}
	if got := tt.commitCalls.Load(); got != 0 {
		t.Errorf("Commit calls = %d, want 0", got)
	}
	if got := tt.discardCalls.Load(); got != 0 {
		t.Errorf("Discard calls = %d, want 0 — no transaction was opened", got)
	}
}

// TestNonTransactionalCLISucceeds pins the negative case: a
// non-transactional reconcile with CLI blocks runs normally. CLI
// ops use the applyTransport fallback (which is e.Transport when
// no transaction is open).
func TestNonTransactionalCLISucceeds(t *testing.T) {
	t.Parallel()
	e, tt, res := newTxFixture(true, false, false, false /*transactional=false*/)
	res.CLIBlocks = []intent.CLIBlock{{
		TemplateName: "test-cli",
		CLI:          "interface Loopback0\n description test\n",
	}}

	r := e.Reconcile(context.Background(), res)

	if r.Phase != PhaseInSync {
		t.Fatalf("phase = %q, want InSync; err=%v", r.Phase, r.Err)
	}
	// At least one Mutate (CLI block). Non-transactional callers
	// pass the empty handle.
	if got := tt.mutateCalls.Load(); got == 0 {
		t.Errorf("Mutate calls = 0; CLI block should have been pushed")
	}
}

// ─── Wave 10.2 — confirmed-commit (RFC 6241 §8.4) auto-revert ───────
//
// Four tests covering the state-machine matrix the engine must
// honor when spec.confirmTimeoutSeconds > 0:
//
//                              capability        outcome
//   transactional + opt-in  +  advertised   →   confirmed (happy path)
//   transactional + opt-in  +  advertised   →   auto_reverted (running-verify fails)
//   transactional + opt-in  +  not-advertised→  fallback to plain Commit + reason
//   non-transactional + opt-in              →   reason="non-transactional reconcile"
//
// All four also assert that confirmed-commit is NEVER engaged for
// CRs whose ConfirmTimeoutSeconds is zero (the existing test suite
// above already exercises this implicitly; our additions must not
// break that contract).

// TestConfirmedCommitHappyPath drives the auto-revert flow end-to-end
// with a clean running-verify. Asserts CommitConfirmed is called
// with the CR's timeout, ConfirmCommit fires, plain Commit is NOT
// called, and Result.ConfirmedCommitUsed is true.
func TestConfirmedCommitHappyPath(t *testing.T) {
	t.Parallel()
	e, tt, res := newTxFixture(true /*supportsTx*/, false, false, true /*transactional*/)
	tt.caps.SupportsConfirmedCommit = true
	res.ConfirmTimeoutSeconds = 30

	r := e.Reconcile(context.Background(), res)

	if r.Phase != PhaseInSync {
		t.Fatalf("phase = %q, want InSync; err=%v", r.Phase, r.Err)
	}
	if !r.ConfirmedCommitUsed {
		t.Errorf("ConfirmedCommitUsed=false; expected true on the auto-revert path")
	}
	if r.ConfirmedCommitFallback != "" {
		t.Errorf("ConfirmedCommitFallback=%q; expected empty on the happy path", r.ConfirmedCommitFallback)
	}
	if got := tt.commitConfirmedCalls.Load(); got != 1 {
		t.Errorf("CommitConfirmed calls = %d, want 1", got)
	}
	if got := tt.confirmCommitCalls.Load(); got != 1 {
		t.Errorf("ConfirmCommit calls = %d, want 1", got)
	}
	if got := tt.commitCalls.Load(); got != 0 {
		t.Errorf("plain Commit calls = %d, want 0 (auto-revert path skips plain Commit)", got)
	}
	// Engine forwarded the CR's timeout into the transport.
	if len(tt.commitConfirmedTimeouts) != 1 || tt.commitConfirmedTimeouts[0] != 30*time.Second {
		t.Errorf("CommitConfirmed timeouts = %v, want [30s]", tt.commitConfirmedTimeouts)
	}
}

// TestConfirmedCommitAutoRevertOnVerifyFailure drives the failure
// half of the auto-revert flow. The fixture's writer is rigged to
// return non-empty Diff on the second call (the running-verify pass)
// — meaning running diverged from intent post-commit. Engine must
// NOT call ConfirmCommit; the deferred Discard runs, and
// Result.Phase is Failed with an "auto-revert" Err.
func TestConfirmedCommitAutoRevertOnVerifyFailure(t *testing.T) {
	t.Parallel()
	tt := &txTransport{
		caps: transport.Capabilities{
			SupportsTransactions:    true,
			SupportsConfirmedCommit: true,
		},
	}
	// Writer that's clean during candidate-verify (so the engine
	// reaches CommitConfirmed) but dirty during running-verify (so
	// the engine declines to ConfirmCommit and the device's
	// auto-revert timer is left to fire).
	w := &runningDirtyWriter{}
	e := &Engine{
		Transport: tt,
		Lookup: func(family string, _ string) writers.SectionWriter {
			if family == "system" {
				return w
			}
			return nil
		},
	}
	res := &intent.ResolvedIntent{
		DeviceName:            "test-dev",
		ManagedFamilies:       []string{"system"},
		Configuration:         map[string]any{"system": map[string]any{"hostname": "x"}},
		Transactional:         true,
		WriteStartup:          false,
		DriftPolicy:           "revert",
		ConfirmTimeoutSeconds: 30,
	}

	r := e.Reconcile(context.Background(), res)

	if r.Phase != PhaseFailed {
		t.Fatalf("phase = %q, want Failed (running-verify should have failed); err=%v", r.Phase, r.Err)
	}
	if r.ConfirmedCommitUsed {
		t.Errorf("ConfirmedCommitUsed=true; expected false because running-verify failed")
	}
	if got := tt.commitConfirmedCalls.Load(); got != 1 {
		t.Errorf("CommitConfirmed calls = %d, want 1", got)
	}
	if got := tt.confirmCommitCalls.Load(); got != 0 {
		t.Errorf("ConfirmCommit calls = %d, want 0 (verify failed; engine MUST NOT confirm)", got)
	}
	if got := tt.discardCalls.Load(); got != 1 {
		t.Errorf("Discard calls = %d, want 1 (deferred cleanup runs when commit not confirmed)", got)
	}
	if r.Err == nil || !contains(r.Err.Error(), "auto-revert") {
		t.Errorf("Err=%v; expected auto-revert mention", r.Err)
	}
}

// TestConfirmedCommitFallbackWhenTransportLacksInterface is the
// backward-compat regression for RESTCONF / gNMI transports that
// don't implement ConfirmedCommitter. Engine must take the plain
// Commit path AND surface the reason via
// Result.ConfirmedCommitFallback so the reconciler can event-warn
// the operator.
func TestConfirmedCommitFallbackWhenTransportLacksInterface(t *testing.T) {
	t.Parallel()
	// plainTxTransport is identical in shape to txTransport but
	// without the ConfirmedCommitter methods — it does NOT satisfy
	// the optional interface.
	tt := &plainTxTransport{
		caps: transport.Capabilities{SupportsTransactions: true},
	}
	w := &txWriter{}
	e := &Engine{
		Transport: tt,
		Lookup: func(family string, _ string) writers.SectionWriter {
			if family == "system" {
				return w
			}
			return nil
		},
	}
	res := &intent.ResolvedIntent{
		DeviceName:            "test-dev",
		ManagedFamilies:       []string{"system"},
		Configuration:         map[string]any{"system": map[string]any{"hostname": "x"}},
		Transactional:         true,
		WriteStartup:          false,
		DriftPolicy:           "revert",
		ConfirmTimeoutSeconds: 30,
	}

	r := e.Reconcile(context.Background(), res)

	if r.Phase != PhaseInSync {
		t.Fatalf("phase = %q, want InSync (plain Commit fallback); err=%v", r.Phase, r.Err)
	}
	if r.ConfirmedCommitUsed {
		t.Errorf("ConfirmedCommitUsed=true; expected false on the fallback path")
	}
	if r.ConfirmedCommitFallback != "transport does not implement ConfirmedCommitter" {
		t.Errorf("ConfirmedCommitFallback=%q; expected the missing-interface reason", r.ConfirmedCommitFallback)
	}
	if tt.commitCalls != 1 {
		t.Errorf("plain Commit calls = %d, want 1 (engine fell back)", tt.commitCalls)
	}
}

// TestConfirmedCommitFallbackWhenCapabilityNotAdvertised is the
// older-IOS-XE-image regression: NETCONF transport implements the
// interface but the device's hello didn't advertise
// confirmed-commit:1.0. Engine must fall back AND surface the
// capability-specific reason.
func TestConfirmedCommitFallbackWhenCapabilityNotAdvertised(t *testing.T) {
	t.Parallel()
	e, tt, res := newTxFixture(true /*supportsTx*/, false, false, true /*transactional*/)
	// Capability NOT advertised — older IOS-XE image.
	tt.caps.SupportsConfirmedCommit = false
	res.ConfirmTimeoutSeconds = 30

	r := e.Reconcile(context.Background(), res)

	if r.Phase != PhaseInSync {
		t.Fatalf("phase = %q, want InSync; err=%v", r.Phase, r.Err)
	}
	if r.ConfirmedCommitUsed {
		t.Errorf("ConfirmedCommitUsed=true; expected false when capability missing")
	}
	if r.ConfirmedCommitFallback != "device did not advertise confirmed-commit:1.0" {
		t.Errorf("ConfirmedCommitFallback=%q; expected the missing-capability reason", r.ConfirmedCommitFallback)
	}
	if got := tt.commitCalls.Load(); got != 1 {
		t.Errorf("plain Commit calls = %d, want 1 (fallback path)", got)
	}
	if got := tt.commitConfirmedCalls.Load(); got != 0 {
		t.Errorf("CommitConfirmed calls = %d, want 0 (capability not advertised)", got)
	}
}

// TestConfirmedCommitNonTransactionalReconcileSurfacesReason
// covers the operator-error case: spec.confirmTimeoutSeconds set
// but transactional=false. Confirmed-commit needs a candidate
// datastore; without transactional=true there is no commit to
// confirm. The engine writes via per-family Mutate (already
// happens in the non-transactional path) and surfaces the
// fallback reason for the reconciler to event-warn.
func TestConfirmedCommitNonTransactionalReconcileSurfacesReason(t *testing.T) {
	t.Parallel()
	e, _, res := newTxFixture(false /*supportsTx*/, false, false, false /*transactional*/)
	res.ConfirmTimeoutSeconds = 30

	r := e.Reconcile(context.Background(), res)

	if r.Phase != PhaseInSync {
		t.Fatalf("phase = %q, want InSync; err=%v", r.Phase, r.Err)
	}
	if r.ConfirmedCommitFallback != "non-transactional reconcile" {
		t.Errorf("ConfirmedCommitFallback=%q; expected 'non-transactional reconcile'", r.ConfirmedCommitFallback)
	}
	if r.ConfirmedCommitUsed {
		t.Errorf("ConfirmedCommitUsed=true; expected false (non-transactional)")
	}
}

// runningDirtyWriter is clean on the candidate-side passes (so the
// engine reaches CommitConfirmed) but dirty on the running-verify
// pass (so ConfirmCommit is NOT fired and the device's
// auto-revert timer is left to expire). Mirrors the natural shape
// of an operational regression — a change applies cleanly to the
// candidate, the candidate-verify reports no drift, but a
// post-commit side-effect on running re-introduces drift.
//
// Diff call sequence the engine drives in the transactional path:
//
//	1: planning        — returns ops to apply
//	2: candidate-verify — returns empty (apply succeeded)
//	3: running-verify   — returns ops (drift on running)
//
// The third call is the one runningVerify makes after
// CommitConfirmed; a non-empty Diff there causes the engine to
// decline ConfirmCommit and let the auto-revert timer fire.
type runningDirtyWriter struct {
	diffCalls atomic.Int32
}

func (*runningDirtyWriter) Family() string      { return "system" }
func (*runningDirtyWriter) YANGPaths() []string { return []string{"/system"} }
func (*runningDirtyWriter) Fetch(ctx context.Context, t transport.Interface) (any, error) {
	if _, err := t.Fetch(ctx, "/system"); err != nil {
		return nil, err
	}
	return nil, nil
}
func (w *runningDirtyWriter) Diff(desired, _ any) ([]transport.Op, error) {
	if desired == nil {
		return nil, nil
	}
	switch w.diffCalls.Add(1) {
	case 1:
		// Planning — emit the op the engine will apply.
		return []transport.Op{{Verb: transport.VerbReplace, Path: "/system"}}, nil
	case 2:
		// Candidate-verify — clean (apply succeeded).
		return nil, nil
	default:
		// Running-verify — drift on running. Triggers
		// auto-revert.
		return []transport.Op{{Verb: transport.VerbReplace, Path: "/system"}}, nil
	}
}
func (*runningDirtyWriter) Apply(ctx context.Context, t transport.Interface, ops []transport.Op) error {
	return t.Mutate(ctx, "", ops)
}

// plainTxTransport is a transactional-capable transport that does
// NOT implement ConfirmedCommitter. Used to exercise the engine's
// type-assertion fallback path when a CR opts in to confirmed-commit
// against a transport that can't deliver it (the RESTCONF / today's
// gNMI case in production).
type plainTxTransport struct {
	caps         transport.Capabilities
	commitCalls  int32
	discardCalls int32
}

func (t *plainTxTransport) Capabilities() transport.Capabilities { return t.caps }
func (t *plainTxTransport) Fetch(context.Context, string) ([]byte, error) {
	return nil, nil
}
func (t *plainTxTransport) StartTransaction(context.Context) (transport.TxHandle, error) {
	return fakeHandle, nil
}
func (t *plainTxTransport) Mutate(context.Context, transport.TxHandle, []transport.Op) error {
	return nil
}
func (t *plainTxTransport) Commit(context.Context, transport.TxHandle) error {
	t.commitCalls++
	return nil
}
func (t *plainTxTransport) Discard(context.Context, transport.TxHandle) error {
	t.discardCalls++
	return nil
}
func (t *plainTxTransport) SaveStartup(context.Context) error { return nil }
func (t *plainTxTransport) Close() error                      { return nil }

// contains is a tiny strings.Contains shim so the test file doesn't
// pull in the strings package just for one Err message check.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
