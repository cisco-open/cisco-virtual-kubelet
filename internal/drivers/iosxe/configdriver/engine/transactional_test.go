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
		Lookup: func(family string) writers.SectionWriter {
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
	e.Lookup = func(family string) writers.SectionWriter {
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

func (failingApplyWriter) Family() string                                    { return "system" }
func (failingApplyWriter) YANGPaths() []string                               { return []string{"/system"} }
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

// TestCLIBlockUsesTransactionalView is the second half of Wave 1A-fu.
// applyCLIBlock previously called e.Transport.Mutate directly — CLI
// ops always wrote running config, even mid-transaction. Wave 1A-fu
// routes them through e.applyTransport so CLI participates in the
// candidate datastore alongside structured family ops.
func TestCLIBlockUsesTransactionalView(t *testing.T) {
	t.Parallel()
	e, tt, res := newTxFixture(true, false, false, true)
	res.CLIBlocks = []intent.CLIBlock{{
		TemplateName: "test-cli",
		CLI:          "interface Loopback0\n description test\n",
	}}

	r := e.Reconcile(context.Background(), res)

	if r.Phase != PhaseInSync {
		t.Fatalf("phase = %q, want InSync; err=%v", r.Phase, r.Err)
	}
	// Every Mutate the engine emitted — family writes plus the CLI
	// block — must carry the captured candidate handle. A direct
	// e.Transport.Mutate call would record the empty handle here.
	if len(tt.mutateHandlesSeen) == 0 {
		t.Fatal("expected at least one Mutate call (family + CLI)")
	}
	cliMutateCount := 0
	for i, h := range tt.mutateHandlesSeen {
		if h != fakeHandle {
			t.Errorf("Mutate[%d] handle = %q, want %q (CLI must use the tx view)", i, h, fakeHandle)
		}
		cliMutateCount++
	}
	if cliMutateCount < 2 {
		t.Errorf("expected >=2 Mutate calls (family + CLI), got %d", cliMutateCount)
	}
}
