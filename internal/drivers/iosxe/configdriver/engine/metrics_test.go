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
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

func TestCapDriftBelowLimit(t *testing.T) {
	// Below-cap input must pass through unchanged so the common
	// path (no truncation) doesn't allocate or rewrite the slice.
	in := []DriftEntry{
		{Family: "vlan", Path: "/v"},
		{Family: "vrf", Path: "/r"},
	}
	out, dropped := CapDrift(in)
	if dropped != 0 {
		t.Fatalf("dropped=%d, want 0", dropped)
	}
	if len(out) != 2 {
		t.Fatalf("len=%d, want 2", len(out))
	}
	if &out[0] != &in[0] {
		t.Errorf("CapDrift should pass slice through unchanged when under cap")
	}
}

func TestCapDriftAtLimit(t *testing.T) {
	// Exactly-at-cap is the corner case where len > cap is false but
	// any off-by-one would either drop one or fail to drop one.
	in := make([]DriftEntry, MaxDriftEntries)
	for i := range in {
		in[i] = DriftEntry{Family: "vlan", Path: fmt.Sprintf("/v/%d", i)}
	}
	out, dropped := CapDrift(in)
	if dropped != 0 {
		t.Fatalf("dropped=%d, want 0", dropped)
	}
	if len(out) != MaxDriftEntries {
		t.Fatalf("len=%d, want %d", len(out), MaxDriftEntries)
	}
}

func TestCapDriftAboveLimit(t *testing.T) {
	// Three entries past the cap exercises the truncation path and
	// pins the dropped count to the exact overflow.
	const overflow = 3
	in := make([]DriftEntry, MaxDriftEntries+overflow)
	for i := range in {
		in[i] = DriftEntry{Family: "vlan", Path: fmt.Sprintf("/v/%d", i)}
	}
	out, dropped := CapDrift(in)
	if dropped != overflow {
		t.Fatalf("dropped=%d, want %d", dropped, overflow)
	}
	if len(out) != MaxDriftEntries {
		t.Fatalf("len=%d, want %d", len(out), MaxDriftEntries)
	}
	// Retained entries must be the head of the input, not a random
	// slice — surprises here would mask which families were kept.
	if out[0].Path != "/v/0" || out[MaxDriftEntries-1].Path != fmt.Sprintf("/v/%d", MaxDriftEntries-1) {
		t.Errorf("retained entries are not the head: first=%q last=%q",
			out[0].Path, out[MaxDriftEntries-1].Path)
	}
}

func TestCapDriftNilInput(t *testing.T) {
	// Nil-in / nil-out keeps the JSON-omitempty story intact for
	// the CR status — a freshly-applied InSync CR shouldn't write
	// "drift: []" into etcd.
	out, dropped := CapDrift(nil)
	if dropped != 0 || out != nil {
		t.Fatalf("nil input: out=%v dropped=%d", out, dropped)
	}
}

func TestRecordDriftTruncatedNoOpWhenUnregistered(t *testing.T) {
	// In unit tests we don't want a hidden requirement to
	// RegisterMetrics first — the engine package's metric helpers
	// must no-op cleanly when their var is nil. This is the safety
	// rail; if it ever panics we'd lose every test that touches
	// the engine without a metrics setup.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RecordDriftTruncated panicked when metrics unregistered: %v", r)
		}
	}()
	RecordDriftTruncated("dev0", 7)
	RecordDriftTruncated("dev0", 0)
	RecordDriftTruncated("dev0", -1)
}

// The three tests below pin the same nil-guard contract for the
// transport-aware counters added per the pre-PR test enrichment
// plan §3. Every helper must accept calls when its underlying
// CounterVec is nil (RegisterMetrics not run) without panicking,
// otherwise existing tests that drive Engine.Reconcile but never
// register metrics would all break.

func TestRecordTransactionNoOpWhenUnregistered(t *testing.T) {
	saved := transactionsTotal
	transactionsTotal = nil
	defer func() { transactionsTotal = saved }()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("recordTransaction panicked when metric nil: %v", r)
		}
	}()
	recordTransaction("dev0", "netconf", "commit")
	recordTransaction("dev0", "netconf", "discard")
}

func TestRecordSaveStartupNoOpWhenUnregistered(t *testing.T) {
	saved := saveStartupTotal
	saveStartupTotal = nil
	defer func() { saveStartupTotal = saved }()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("recordSaveStartup panicked when metric nil: %v", r)
		}
	}()
	recordSaveStartup("dev0", "restconf", "ok")
}

func TestRecordMutateOpsNoOpWhenUnregistered(t *testing.T) {
	saved := mutateOpsTotal
	mutateOpsTotal = nil
	defer func() { mutateOpsTotal = saved }()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("recordMutateOps panicked when metric nil: %v", r)
		}
	}()
	recordMutateOps("dev0", "gnmi", []transport.Op{
		{Verb: transport.VerbReplace},
		{Verb: transport.VerbMerge},
	})
	// Empty slice path.
	recordMutateOps("dev0", "gnmi", nil)
}

// TestTransportAwareCountersWireOnTransactionalSuccess is the
// headline wiring test for the pre-PR test enrichment plan §3.
// It drives Engine.Reconcile through a clean transactional +
// writeStartup flow and asserts that:
//
//	transactionsTotal{outcome=commit}     increments by 1
//	saveStartupTotal {outcome=ok}         increments by 1
//	mutateOpsTotal   {verb=REPLACE}       increments by 1
//
// All three are labelled with transport=netconf because the
// fixture's Capabilities sets Kind=KindNETCONF. Pre-fix the
// counters did not exist, so live tests had no metric-level proof
// that the intended transport actually performed the write —
// they could only assert "device ended up with the right state",
// which a silently RESTCONF-fallback could also produce.
//
// The test stashes the package-global metric vars, swaps in
// freshly-constructed CounterVecs registered on a private
// prometheus.Registry, runs the reconcile, reads counter values
// via testutil.ToFloat64, then restores the originals. This
// mirrors the transport package's metrics_test.go pattern and
// avoids the metricsOnce gate.
func TestTransportAwareCountersWireOnTransactionalSuccess(t *testing.T) {
	// Stash + restore. Done eagerly so a panic mid-test still
	// restores the package state (defer below restores).
	savedTx := transactionsTotal
	savedSS := saveStartupTotal
	savedMO := mutateOpsTotal
	defer func() {
		transactionsTotal = savedTx
		saveStartupTotal = savedSS
		mutateOpsTotal = savedMO
	}()

	reg := prometheus.NewRegistry()
	transactionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "test_transactions_total"},
		[]string{"device", "transport", "outcome"},
	)
	saveStartupTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "test_save_startup_total"},
		[]string{"device", "transport", "outcome"},
	)
	mutateOpsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "test_mutate_ops_total"},
		[]string{"device", "transport", "verb"},
	)
	reg.MustRegister(transactionsTotal, saveStartupTotal, mutateOpsTotal)

	// Build the standard transactional fixture and force the
	// transport's Kind so the metric labels carry "netconf"
	// (the txTransport stub doesn't set Kind by default).
	e, tt, res := newTxFixture(true /*supportsTx*/, true /*supportsSave*/, true /*writeStartup*/, true /*transactional*/)
	tt.caps.Kind = transport.KindNETCONF

	r := e.Reconcile(context.Background(), res)

	if r.Phase != PhaseInSync {
		t.Fatalf("phase = %q, want InSync; err=%v", r.Phase, r.Err)
	}
	if !r.SaveStartupOK {
		t.Fatalf("SaveStartupOK = false; expected save-startup to have succeeded for the writeStartup=true fixture")
	}

	// Headline assertions: each new counter has a non-zero value
	// for the expected label set.
	if got := testutil.ToFloat64(transactionsTotal.WithLabelValues("test-dev", "netconf", "commit")); got != 1 {
		t.Errorf("transactions_total{commit,netconf} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(transactionsTotal.WithLabelValues("test-dev", "netconf", "discard")); got != 0 {
		t.Errorf("transactions_total{discard,netconf} = %v, want 0 on a clean commit", got)
	}
	if got := testutil.ToFloat64(saveStartupTotal.WithLabelValues("test-dev", "netconf", "ok")); got != 1 {
		t.Errorf("save_startup_total{ok,netconf} = %v, want 1", got)
	}
	// txWriter emits one VerbReplace op on the first Diff. The
	// counter is incremented before Apply, so a clean reconcile
	// records exactly one REPLACE.
	if got := testutil.ToFloat64(mutateOpsTotal.WithLabelValues("test-dev", "netconf", string(transport.VerbReplace))); got != 1 {
		t.Errorf("mutate_ops_total{REPLACE,netconf} = %v, want 1", got)
	}
}

// TestTransportAwareCountersDiscardPathOnCommitFailure exercises
// the alternate transactional outcome — the commit RPC fails, the
// engine flips Phase=Failed, and the deferred Discard runs. Both
// commit_failed and discard outcomes should increment.
func TestTransportAwareCountersDiscardPathOnCommitFailure(t *testing.T) {
	savedTx := transactionsTotal
	defer func() { transactionsTotal = savedTx }()

	reg := prometheus.NewRegistry()
	transactionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "test_tx_total_v2"},
		[]string{"device", "transport", "outcome"},
	)
	reg.MustRegister(transactionsTotal)

	e, tt, res := newTxFixture(true, false, false, true)
	tt.caps.Kind = transport.KindNETCONF
	tt.commitErr = fmt.Errorf("simulated commit RPC failure")

	r := e.Reconcile(context.Background(), res)

	if r.Phase != PhaseFailed {
		t.Fatalf("phase = %q, want Failed (commit returned error); err=%v", r.Phase, r.Err)
	}
	if got := testutil.ToFloat64(transactionsTotal.WithLabelValues("test-dev", "netconf", "commit_failed")); got != 1 {
		t.Errorf("transactions_total{commit_failed} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(transactionsTotal.WithLabelValues("test-dev", "netconf", "discard")); got != 1 {
		t.Errorf("transactions_total{discard} = %v, want 1 (deferred cleanup ran)", got)
	}
	if got := testutil.ToFloat64(transactionsTotal.WithLabelValues("test-dev", "netconf", "commit")); got != 0 {
		t.Errorf("transactions_total{commit} = %v, want 0 on commit failure", got)
	}
}
