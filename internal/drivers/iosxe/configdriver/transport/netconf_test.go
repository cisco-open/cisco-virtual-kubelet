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
