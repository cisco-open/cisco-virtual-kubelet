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

package transport

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockDevice is a scriptable NETCONF peer: it exposes an
// io.ReadWriteCloser that the transport dials against, reads each
// framed request, and emits a canned response matching a regex.
// Not a full YANG server — we just need deterministic responses
// for the RPC shapes the transport builds.
type mockDevice struct {
	// cli is the transport-facing end. The transport writes here
	// and reads here; the mock reads/writes the server-facing end.
	cli io.ReadWriteCloser

	// closers for teardown.
	closers []io.Closer

	// script is the list of (expected request substring,
	// response payload) pairs consumed in order. A request that
	// does not contain the expected substring fails the test.
	script []scriptStep

	// Keeping a sync-linked failure channel so the mock goroutine
	// surfaces assertion errors back to the test goroutine.
	failures chan string

	t *testing.T
}

type scriptStep struct {
	// expect is a substring that must appear in the request
	// body. Empty string matches anything (useful for the
	// hello exchange which we don't need to validate byte-for-
	// byte in every test).
	expect string
	// reply is the response payload (without framing). The mock
	// frames it with ]]>]]>.
	reply string
}

// newMockDevice wires an in-memory pipe pair and starts the
// mock goroutine. Close cleans everything up.
func newMockDevice(t *testing.T, script []scriptStep) *mockDevice {
	t.Helper()
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()

	m := &mockDevice{
		cli:      &pipeConn{r: clientR, w: clientW},
		closers:  []io.Closer{clientR, clientW, serverR, serverW},
		script:   script,
		failures: make(chan string, 16),
		t:        t,
	}

	go m.serve(serverR, serverW)
	return m
}

func (m *mockDevice) serve(r io.Reader, w io.Writer) {
	br := bufio.NewReader(r)

	// Hello is symmetric in NETCONF — both peers write first.
	// Over io.Pipe a naive write-then-read on both sides
	// deadlocks because the pipe has no buffer. Drive write and
	// read concurrently; wait for both to finish before moving
	// on to the scripted RPCs.
	serverHello := `<?xml version="1.0" encoding="UTF-8"?>
<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">
  <capabilities>
    <capability>urn:ietf:params:netconf:base:1.0</capability>
    <capability>urn:ietf:params:netconf:capability:writable-running:1.0</capability>
    <capability>urn:ietf:params:netconf:capability:candidate:1.0</capability>
    <capability>urn:ietf:params:netconf:capability:confirmed-commit:1.0</capability>
  </capabilities>
  <session-id>42</session-id>
</hello>`
	helloDone := make(chan struct{})
	go func() {
		defer close(helloDone)
		if err := writeFrame10(w, []byte(serverHello)); err != nil {
			m.failures <- "mock: write hello: " + err.Error()
		}
	}()
	if _, err := readFrame10(br); err != nil {
		m.failures <- "mock: read client hello: " + err.Error()
		<-helloDone
		return
	}
	<-helloDone

	for _, step := range m.script {
		req, err := readFrame10(br)
		if err != nil {
			m.failures <- "mock: read request: " + err.Error()
			return
		}
		if step.expect != "" && !bytes.Contains(req, []byte(step.expect)) {
			m.failures <- "mock: expected " + step.expect + " in request, got " + string(req)
			return
		}
		if err := writeFrame10(w, []byte(step.reply)); err != nil {
			m.failures <- "mock: write reply: " + err.Error()
			return
		}
	}
}

// assertNoFailures drains the failure channel and fails the test
// if anything landed in it during the mock's lifetime.
func (m *mockDevice) assertNoFailures() {
	m.t.Helper()
	// Give the goroutine a beat to surface any pending failure.
	time.Sleep(10 * time.Millisecond)
	select {
	case msg := <-m.failures:
		m.t.Errorf("mock device: %s", msg)
	default:
	}
}

func (m *mockDevice) close() {
	for _, c := range m.closers {
		_ = c.Close()
	}
}

// pipeConn turns a paired io.PipeReader + io.PipeWriter into a
// ReadWriteCloser suitable for NewNETCONF.
type pipeConn struct {
	r      *io.PipeReader
	w      *io.PipeWriter
	mu     sync.Mutex
	closed bool
}

func (p *pipeConn) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeConn) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeConn) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	_ = p.r.Close()
	_ = p.w.Close()
	return nil
}

// ---------------------------------------------------------------
// Tests
// ---------------------------------------------------------------

func TestNETCONFHelloAdvertisesTransactions(t *testing.T) {
	m := newMockDevice(t, nil)
	defer m.close()

	ti, err := NewNETCONF(NETCONFConfig{Conn: m.cli})
	if err != nil {
		t.Fatalf("NewNETCONF: %v", err)
	}
	defer ti.Close()

	caps := ti.Capabilities()
	if caps.Kind != KindNETCONF {
		t.Errorf("kind=%v", caps.Kind)
	}
	if !caps.SupportsTransactions {
		t.Errorf("SupportsTransactions=false; mock advertises candidate")
	}
	m.assertNoFailures()
}

func TestNETCONFFetchRoundTrip(t *testing.T) {
	m := newMockDevice(t, []scriptStep{
		{
			expect: "<get-config>",
			reply: `<rpc-reply message-id="101" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">
  <data>
    <native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native">
      <vlan>
        <vlan-list xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-vlan">
          <id>10</id>
          <name>users</name>
        </vlan-list>
      </vlan>
    </native>
  </data>
</rpc-reply>`,
		},
	})
	defer m.close()

	ti, err := NewNETCONF(NETCONFConfig{Conn: m.cli})
	if err != nil {
		t.Fatalf("NewNETCONF: %v", err)
	}
	defer ti.Close()

	body, err := ti.Fetch(context.Background(),
		"/Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// The XML→JSON converter should have produced a JSON object
	// whose first key is the Cisco-IOS-XE-native:native envelope.
	if !strings.Contains(string(body), "Cisco-IOS-XE-native:native") {
		t.Errorf("expected envelope key in JSON; body=%s", body)
	}
	if !strings.Contains(string(body), "users") {
		t.Errorf("expected vlan name in JSON; body=%s", body)
	}
	m.assertNoFailures()
}

// TestNETCONFFetchTxReadsCandidate is the Wave 1A-fu regression
// against follow-up Finding #1: NETCONF must implement TxFetcher
// and route candidate-handle reads to <get-config><source><candidate/>>
// so the engine's verify-Fetch sees the in-flight transaction's
// writes. Pre-fix, FetchTx didn't exist and the engine read
// running mid-transaction, missing every pending edit.
func TestNETCONFFetchTxReadsCandidate(t *testing.T) {
	m := newMockDevice(t, []scriptStep{
		{
			expect: "<source><candidate/></source>",
			reply: `<rpc-reply message-id="101" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">
  <data>
    <native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native">
      <hostname>candidate-host</hostname>
    </native>
  </data>
</rpc-reply>`,
		},
	})
	defer m.close()

	ti, err := NewNETCONF(NETCONFConfig{Conn: m.cli})
	if err != nil {
		t.Fatalf("NewNETCONF: %v", err)
	}
	defer ti.Close()

	tf, ok := ti.(TxFetcher)
	if !ok {
		t.Fatalf("netconfTransport must implement TxFetcher")
	}
	body, err := tf.FetchTx(context.Background(), TxHandle("candidate"),
		"/Cisco-IOS-XE-native:native/hostname")
	if err != nil {
		t.Fatalf("FetchTx: %v", err)
	}
	if !strings.Contains(string(body), "candidate-host") {
		t.Errorf("expected candidate-shaped response, got %s", body)
	}
	m.assertNoFailures()
}

func TestNETCONFTransactionalApply(t *testing.T) {
	// The sequence the engine produces under
	// spec.transactional: true is lock → edit-config → commit →
	// unlock. Wire the mock to ack each one and verify we end
	// up with an InSync commit.
	m := newMockDevice(t, []scriptStep{
		{expect: "<lock>", reply: `<rpc-reply message-id="101" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`},
		{expect: "<edit-config>", reply: `<rpc-reply message-id="102" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`},
		{expect: "<commit/>", reply: `<rpc-reply message-id="103" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`},
		{expect: "<unlock>", reply: `<rpc-reply message-id="104" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`},
	})
	defer m.close()

	ti, err := NewNETCONF(NETCONFConfig{Conn: m.cli})
	if err != nil {
		t.Fatalf("NewNETCONF: %v", err)
	}
	defer ti.Close()

	ctx := context.Background()
	tx, err := ti.StartTransaction(ctx)
	if err != nil {
		t.Fatalf("StartTransaction: %v", err)
	}
	if tx == "" {
		t.Fatal("StartTransaction returned empty handle")
	}

	ops := []Op{
		{Verb: VerbMerge, Path: "/Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list=10",
			Body: []byte(`{"Cisco-IOS-XE-vlan:vlan-list":[{"id":10,"name":"users"}]}`)},
	}
	if err := ti.Mutate(ctx, tx, ops); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if err := ti.Commit(ctx, tx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	m.assertNoFailures()
}

// TestNETCONFMutateAutoPromotesOnCandidateOnly is a regression test
// for the live retest finding: enabling
// `netconf-yang feature candidate-datastore` on IOS-XE 17.x drops
// the device's :writable-running:1.0 capability advertisement and
// makes direct <edit-config target=running> fail with
// `Unsupported capability :writable-running`. Non-transactional
// callers (tx="") must be auto-promoted to an implicit
// lock(candidate) + edit-config(candidate) + commit + unlock cycle
// so the engine's non-transactional path keeps working without
// knowing about the device-mode shift.
func TestNETCONFMutateAutoPromotesOnCandidateOnly(t *testing.T) {
	// Mock advertises :candidate:1.0 but NOT :writable-running:1.0
	// — the candidate-only state.
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	cli := &pipeConn{r: clientR, w: clientW}
	defer cli.Close()
	defer serverR.Close()
	defer serverW.Close()

	candidateOnlyHello := `<?xml version="1.0" encoding="UTF-8"?>
<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">
  <capabilities>
    <capability>urn:ietf:params:netconf:base:1.0</capability>
    <capability>urn:ietf:params:netconf:capability:candidate:1.0</capability>
  </capabilities>
  <session-id>1</session-id>
</hello>`
	expectations := []scriptStep{
		{expect: "<lock>", reply: `<rpc-reply message-id="101" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`},
		{expect: "<edit-config><target><candidate/></target>", reply: `<rpc-reply message-id="102" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`},
		{expect: "<commit/>", reply: `<rpc-reply message-id="103" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`},
		{expect: "<unlock>", reply: `<rpc-reply message-id="104" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`},
	}
	failures := make(chan string, 8)
	go func() {
		br := bufio.NewReader(serverR)
		// Hello exchange.
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = writeFrame10(serverW, []byte(candidateOnlyHello))
		}()
		_, _ = readFrame10(br)
		<-done
		// Scripted RPC handling.
		for _, step := range expectations {
			req, rerr := readFrame10(br)
			if rerr != nil {
				failures <- fmt.Sprintf("read rpc: %v", rerr)
				return
			}
			if !strings.Contains(string(req), step.expect) {
				failures <- fmt.Sprintf("expected %q, got %s", step.expect, string(req))
				return
			}
			if werr := writeFrame10(serverW, []byte(step.reply)); werr != nil {
				failures <- fmt.Sprintf("write reply: %v", werr)
				return
			}
		}
	}()

	ti, err := NewNETCONF(NETCONFConfig{Conn: cli})
	if err != nil {
		t.Fatalf("NewNETCONF: %v", err)
	}
	defer ti.Close()
	if ti.Capabilities().SupportsWritableRunning {
		t.Fatal("test setup: candidate-only mock should report SupportsWritableRunning=false")
	}
	if !ti.Capabilities().SupportsTransactions {
		t.Fatal("test setup: candidate-only mock should report SupportsTransactions=true")
	}
	ops := []Op{
		{Verb: VerbMerge, Path: "/Cisco-IOS-XE-native:native/banner",
			Body: []byte(`{"Cisco-IOS-XE-native:banner":{"motd":{"banner":"hello"}}}`)},
	}
	if merr := ti.Mutate(context.Background(), "", ops); merr != nil {
		t.Fatalf("Mutate(implicit-tx): %v", merr)
	}
	select {
	case f := <-failures:
		t.Fatalf("mock device assertion: %s", f)
	default:
	}
}

func TestNETCONFDiscardAfterEditFailure(t *testing.T) {
	// Simulates the engine's cleanup path when Mutate errors.
	// lock → edit-config (rpc-error) → discard → unlock.
	m := newMockDevice(t, []scriptStep{
		{expect: "<lock>", reply: `<rpc-reply message-id="101" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`},
		{
			expect: "<edit-config>",
			reply: `<rpc-reply message-id="102" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">
  <rpc-error>
    <error-type>application</error-type>
    <error-tag>operation-failed</error-tag>
    <error-severity>error</error-severity>
    <error-message>unsupported object</error-message>
  </rpc-error>
</rpc-reply>`,
		},
		{expect: "<discard-changes/>", reply: `<rpc-reply message-id="103" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`},
		{expect: "<unlock>", reply: `<rpc-reply message-id="104" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`},
	})
	defer m.close()

	ti, err := NewNETCONF(NETCONFConfig{Conn: m.cli})
	if err != nil {
		t.Fatalf("NewNETCONF: %v", err)
	}
	defer ti.Close()

	ctx := context.Background()
	tx, _ := ti.StartTransaction(ctx)

	err = ti.Mutate(ctx, tx,
		[]Op{{Verb: VerbMerge, Path: "/Cisco-IOS-XE-native:native/vlan", Body: []byte(`{}`)}})
	if err == nil || !strings.Contains(err.Error(), "unsupported object") {
		t.Fatalf("Mutate: expected rpc-error surface, got %v", err)
	}
	if err := ti.Discard(ctx, tx); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	m.assertNoFailures()
}

func TestNETCONFCLIPushViaCiscoIA(t *testing.T) {
	// VerbCLI bypasses edit-config and invokes cli-config-data
	// directly. Verify the mock receives the Cisco-IA RPC and
	// the <cmd> entries the CLI template produced.
	m := newMockDevice(t, []scriptStep{
		{
			expect: `<cli-config-data xmlns="http://cisco.com/yang/cisco-ia">`,
			reply:  `<rpc-reply message-id="101" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`,
		},
	})
	defer m.close()

	ti, err := NewNETCONF(NETCONFConfig{Conn: m.cli})
	if err != nil {
		t.Fatalf("NewNETCONF: %v", err)
	}
	defer ti.Close()

	cli := "interface Loopback100\n ip address 10.255.100.1 255.255.255.255\n no shutdown"
	err = ti.Mutate(context.Background(), "",
		[]Op{{Verb: VerbCLI, Body: []byte(cli)}})
	if err != nil {
		t.Fatalf("Mutate(CLI): %v", err)
	}
	m.assertNoFailures()
}

func TestNETCONFStartTransactionNotSupportedRejects(t *testing.T) {
	// Mock that only advertises base:1.0 (no candidate). Have
	// to rewire the mock because it hard-codes the capability
	// list.
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	cli := &pipeConn{r: clientR, w: clientW}
	defer cli.Close()
	defer serverR.Close()
	defer serverW.Close()

	go func() {
		br := bufio.NewReader(serverR)
		noCandidate := `<?xml version="1.0" encoding="UTF-8"?>
<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">
  <capabilities>
    <capability>urn:ietf:params:netconf:base:1.0</capability>
  </capabilities>
  <session-id>42</session-id>
</hello>`
		done := make(chan struct{})
		go func() {
			_ = writeFrame10(serverW, []byte(noCandidate))
			close(done)
		}()
		_, _ = readFrame10(br) // discard client hello
		<-done
	}()

	ti, err := NewNETCONF(NETCONFConfig{Conn: cli})
	if err != nil {
		t.Fatalf("NewNETCONF: %v", err)
	}
	defer ti.Close()

	if ti.Capabilities().SupportsTransactions {
		t.Fatal("SupportsTransactions=true when server did not advertise candidate")
	}
	if _, err := ti.StartTransaction(context.Background()); err == nil {
		t.Fatal("StartTransaction: expected ErrUnsupported")
	}
}

// TestNETCONFCapabilitiesReportConfirmedCommit pins the Wave 10
// capability surface: when the server advertises
// urn:ietf:params:netconf:capability:confirmed-commit:1.0 in
// hello (the mock does), Capabilities.SupportsConfirmedCommit
// must be true. The engine consults this flag before dispatching
// the auto-revert flow; a regression here silently degrades every
// confirmed-commit-enabled CR to plain Commit.
func TestNETCONFCapabilitiesReportConfirmedCommit(t *testing.T) {
	m := newMockDevice(t, nil)
	defer m.close()

	ti, err := NewNETCONF(NETCONFConfig{Conn: m.cli})
	if err != nil {
		t.Fatalf("NewNETCONF: %v", err)
	}
	defer ti.Close()

	caps := ti.Capabilities()
	if !caps.SupportsConfirmedCommit {
		t.Fatal("SupportsConfirmedCommit=false; mock advertised confirmed-commit:1.0")
	}
	// Type assertion: the netconf transport MUST implement the
	// optional ConfirmedCommitter interface so the engine's
	// runtime type-assertion finds it.
	if _, ok := ti.(ConfirmedCommitter); !ok {
		t.Fatal("netconf transport does not satisfy ConfirmedCommitter")
	}
	m.assertNoFailures()
}

// TestNETCONFCommitConfirmedRoundTrip pins the wire-level shape
// of the CommitConfirmed → ConfirmCommit sequence: a tentative
// <commit><confirmed/><confirm-timeout>...</confirm-timeout></commit>
// followed by a plain <commit/> followed by an unlock. This is
// the happy-path flow the engine drives after a successful
// running-Verify.
func TestNETCONFCommitConfirmedRoundTrip(t *testing.T) {
	m := newMockDevice(t, []scriptStep{
		{expect: "<lock>", reply: `<rpc-reply message-id="101" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`},
		{
			expect: "<confirm-timeout>30</confirm-timeout>",
			reply:  `<rpc-reply message-id="102" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`,
		},
		{expect: "<commit/>", reply: `<rpc-reply message-id="103" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`},
		{expect: "<unlock>", reply: `<rpc-reply message-id="104" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`},
	})
	defer m.close()

	ti, err := NewNETCONF(NETCONFConfig{Conn: m.cli})
	if err != nil {
		t.Fatalf("NewNETCONF: %v", err)
	}
	defer ti.Close()

	tx, err := ti.StartTransaction(context.Background())
	if err != nil {
		t.Fatalf("StartTransaction: %v", err)
	}
	cc, ok := ti.(ConfirmedCommitter)
	if !ok {
		t.Fatalf("transport does not satisfy ConfirmedCommitter")
	}
	if err := cc.CommitConfirmed(context.Background(), tx, 30*time.Second); err != nil {
		t.Fatalf("CommitConfirmed: %v", err)
	}
	if err := cc.ConfirmCommit(context.Background()); err != nil {
		t.Fatalf("ConfirmCommit: %v", err)
	}
	m.assertNoFailures()
}

// TestNETCONFCommitConfirmedClampsTimeout pins the defensive
// transport-level clamp: a 0-second or negative timeout becomes
// 1s; a 1000s timeout becomes 600s. The engine's per-CR knob is
// already capped at 300 by kubebuilder, but the transport guard
// is defense-in-depth against future callers that bypass the
// schema (e.g. a stand-alone integration test or a future driver
// that sets the timeout directly).
func TestNETCONFCommitConfirmedClampsTimeout(t *testing.T) {
	m := newMockDevice(t, []scriptStep{
		{expect: "<lock>", reply: `<rpc-reply message-id="101" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`},
		{
			expect: "<confirm-timeout>1</confirm-timeout>",
			reply:  `<rpc-reply message-id="102" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`,
		},
		{expect: "<lock>", reply: `<rpc-reply message-id="103" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`},
		{
			expect: "<confirm-timeout>600</confirm-timeout>",
			reply:  `<rpc-reply message-id="104" xmlns="urn:ietf:params:xml:ns:netconf:base:1.0"><ok/></rpc-reply>`,
		},
	})
	defer m.close()

	ti, err := NewNETCONF(NETCONFConfig{Conn: m.cli})
	if err != nil {
		t.Fatalf("NewNETCONF: %v", err)
	}
	defer ti.Close()

	cc, ok := ti.(ConfirmedCommitter)
	if !ok {
		t.Fatalf("transport does not satisfy ConfirmedCommitter")
	}

	// Below-clamp: 0s should become 1s.
	tx, err := ti.StartTransaction(context.Background())
	if err != nil {
		t.Fatalf("StartTransaction (low): %v", err)
	}
	if err := cc.CommitConfirmed(context.Background(), tx, 0); err != nil {
		t.Fatalf("CommitConfirmed (low): %v", err)
	}
	// Above-clamp: 1000s should become 600s.
	tx, err = ti.StartTransaction(context.Background())
	if err != nil {
		t.Fatalf("StartTransaction (high): %v", err)
	}
	if err := cc.CommitConfirmed(context.Background(), tx, 1000*time.Second); err != nil {
		t.Fatalf("CommitConfirmed (high): %v", err)
	}
	m.assertNoFailures()
}

// TestNETCONFCommitConfirmedRejectedWhenServerLacksCapability is
// the backward-compat regression: when the server did NOT
// advertise confirmed-commit:1.0 in hello, the transport-level
// CommitConfirmed must refuse rather than emit a malformed RPC
// the device might error-respond to in unpredictable ways. The
// engine's primary guard is the SupportsConfirmedCommit flag, but
// this transport-side check is defense-in-depth.
func TestNETCONFCommitConfirmedRejectedWhenServerLacksCapability(t *testing.T) {
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	cli := &pipeConn{r: clientR, w: clientW}
	defer cli.Close()
	defer serverR.Close()
	defer serverW.Close()

	go func() {
		br := bufio.NewReader(serverR)

		// Server hello WITHOUT confirmed-commit capability — only
		// base + candidate.
		noConfirm := `<?xml version="1.0" encoding="UTF-8"?>
<hello xmlns="urn:ietf:params:xml:ns:netconf:base:1.0">
  <capabilities>
    <capability>urn:ietf:params:netconf:base:1.0</capability>
    <capability>urn:ietf:params:netconf:capability:candidate:1.0</capability>
  </capabilities>
  <session-id>42</session-id>
</hello>`
		done := make(chan struct{})
		go func() {
			_ = writeFrame10(serverW, []byte(noConfirm))
			close(done)
		}()
		_, _ = readFrame10(br) // discard client hello
		<-done
		// We don't expect any RPC after this — the test asserts
		// CommitConfirmed errors before sending.
	}()

	ti, err := NewNETCONF(NETCONFConfig{Conn: cli})
	if err != nil {
		t.Fatalf("NewNETCONF: %v", err)
	}
	defer ti.Close()

	if ti.Capabilities().SupportsConfirmedCommit {
		t.Fatal("SupportsConfirmedCommit=true when server did not advertise the capability")
	}
	cc, ok := ti.(ConfirmedCommitter)
	if !ok {
		t.Fatal("transport does not satisfy ConfirmedCommitter (interface should still be implementable; the runtime check is on Capabilities)")
	}
	if err := cc.CommitConfirmed(context.Background(), "candidate", 30*time.Second); err == nil {
		t.Fatal("CommitConfirmed: expected error when server lacks the capability")
	}
}

// TestPathToSubtreeFilterWithBodyUnwrapsEnvelope is a regression test
// for the live-device finding (#6(a) follow-on): writers emit a
// RESTCONF-shaped payload with the resource name as the outer JSON
// key (e.g. `{"Cisco-IOS-XE-native:banner": {"motd": {...}}}`).
// pathToSubtreeFilterWithBody then nests this body inside the last
// path segment — for path `/Cisco-IOS-XE-native:native/banner` the
// generated XML used to read `<native><banner><banner><motd>...`,
// which IOS-XE rejected with `unknown-element` because of the
// duplicate `banner` element. The unwrap shim strips the envelope
// when its name matches the last path segment.
func TestPathToSubtreeFilterWithBodyUnwrapsEnvelope(t *testing.T) {
	body := []byte(`{"Cisco-IOS-XE-native:banner":{"motd":{"banner":"hello"}}}`)
	xmlOut, err := pathToSubtreeFilterWithBody(
		"/Cisco-IOS-XE-native:native/banner", body, "merge")
	if err != nil {
		t.Fatalf("pathToSubtreeFilterWithBody: %v", err)
	}
	// The duplicate-banner shape pre-fix produced
	// `<native...><banner ...><banner><motd>...`. Post-fix the inner
	// `<banner>` envelope is gone and the body sits directly under
	// the path-built `<banner>` element.
	if strings.Contains(xmlOut, "<banner><banner>") {
		t.Fatalf("output still has duplicate <banner><banner>: %s", xmlOut)
	}
	if !strings.Contains(xmlOut, "<motd>") {
		t.Fatalf("output missing <motd> child: %s", xmlOut)
	}
}

// TestPathToSubtreeFilterWithBodyKeepsNonMatchingEnvelope verifies
// the unwrap is targeted: when the body's outer key does NOT match
// the last path segment, the body is passed through verbatim. This
// is the keyed-list shape — path `/native/vlan/vlan-list=10` with
// body `{"vlan-list":[{...}]}` keeps the explicit list element so
// the device sees `<vlan-list>...</vlan-list>` (singular) inside
// the list path. (This shape is built by writers that don't go
// through the singleton-envelope path; the test pins the behavior.)
func TestPathToSubtreeFilterWithBodyKeepsNonMatchingEnvelope(t *testing.T) {
	body := []byte(`{"some-other-key":{"x":1}}`)
	xmlOut, err := pathToSubtreeFilterWithBody(
		"/Cisco-IOS-XE-native:native/banner", body, "merge")
	if err != nil {
		t.Fatalf("pathToSubtreeFilterWithBody: %v", err)
	}
	// The non-matching envelope key stays in the output.
	if !strings.Contains(xmlOut, "<some-other-key>") {
		t.Fatalf("non-matching envelope was unexpectedly stripped: %s", xmlOut)
	}
}
