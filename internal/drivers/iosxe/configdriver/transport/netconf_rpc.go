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
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// NETCONF base namespace per RFC 6241.
const netconfBase10 = "urn:ietf:params:xml:ns:netconf:base:1.0"

// capability URIs that we care about.
const (
	capBase10  = "urn:ietf:params:netconf:base:1.0"
	capBase11  = "urn:ietf:params:netconf:base:1.1"
	capCand    = "urn:ietf:params:netconf:capability:candidate:1.0"
	capConfirm = "urn:ietf:params:netconf:capability:confirmed-commit:1.0"
)

// netconfSession is the protocol-level side of the transport. It
// speaks NETCONF over an injected io.ReadWriteCloser so tests can
// run against a pipe, not an SSH channel. The SSH layer (netconf.go)
// wraps this with a connect-time dialer.
type netconfSession struct {
	rw     io.ReadWriteCloser
	br     *bufio.Reader
	mu     sync.Mutex // serialises RPCs — NETCONF is full-duplex but our API is not
	nextID atomic.Int64

	// chunked is true after a successful hello exchange in which
	// both peers advertised base:1.1. Reads and writes below hello
	// always use 1.0 framing per RFC 6242.
	chunked bool

	// serverCaps is the set of capability URIs the server sent.
	// Used by Capabilities() to report transactional / save-startup
	// availability honestly.
	serverCaps map[string]struct{}
}

func newNetconfSession(rw io.ReadWriteCloser) *netconfSession {
	s := &netconfSession{
		rw: rw,
		br: bufio.NewReader(rw),
	}
	// RFC 6241 §4.1: message-id is a string; we use a
	// monotonically-increasing int rendered as decimal.
	s.nextID.Store(100)
	return s
}

// hello exchanges capability lists with the server. Must be the
// first thing run on a fresh session.
func (s *netconfSession) hello(ourCaps []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Write our hello (always in 1.0 framing).
	hello := buildHello(ourCaps)
	if err := writeFrame10(s.rw, hello); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}
	// Read server hello.
	raw, err := readFrame10(s.br)
	if err != nil {
		return fmt.Errorf("read hello: %w", err)
	}
	caps, sessionID, err := parseHello(raw)
	if err != nil {
		return fmt.Errorf("parse hello: %w", err)
	}
	if sessionID == 0 {
		return fmt.Errorf("server hello missing session-id")
	}
	s.serverCaps = caps

	// If both sides advertise base:1.1 we upgrade to chunked
	// framing for every subsequent message, per RFC 6242.
	_, weDoChunked := indexOf(ourCaps, capBase11)
	_, theyDoChunked := caps[capBase11]
	if weDoChunked && theyDoChunked {
		s.chunked = true
	}
	return nil
}

// rpc sends the inner RPC content (without <rpc> wrapper) and
// returns the decoded rpc-reply. Exposed at the session level so
// the NETCONF transport can build each RPC as an XML string —
// Cisco-IA's cli-config-data is the one that doesn't fit the
// schema-driven builders.
func (s *netconfSession) rpc(inner string) (*rpcReply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.nextID.Add(1)
	envelope := fmt.Sprintf(
		`<rpc message-id="%d" xmlns="%s">%s</rpc>`, id, netconfBase10, inner)

	if err := s.writeFrame([]byte(envelope)); err != nil {
		return nil, fmt.Errorf("send rpc %d: %w", id, err)
	}
	body, err := s.readFrame()
	if err != nil {
		return nil, fmt.Errorf("read reply %d: %w", id, err)
	}
	reply, err := parseRPCReply(body)
	if err != nil {
		return nil, err
	}
	if reply.MessageID != "" && reply.MessageID != fmt.Sprintf("%d", id) {
		return nil, fmt.Errorf("reply message-id mismatch: got %q, want %d",
			reply.MessageID, id)
	}
	if len(reply.Errors) > 0 {
		return reply, formatRPCErrors(reply.Errors)
	}
	return reply, nil
}

func (s *netconfSession) writeFrame(p []byte) error {
	if s.chunked {
		return writeFrame11(s.rw, p)
	}
	return writeFrame10(s.rw, p)
}

func (s *netconfSession) readFrame() ([]byte, error) {
	if s.chunked {
		return readFrame11(s.br)
	}
	return readFrame10(s.br)
}

// close sends <close-session/> and tears down the transport. Safe
// to call multiple times; second-and-later calls are no-ops.
//
// The close-session RPC is best-effort and time-bounded: a cranky
// device that stops servicing RPCs must not block teardown
// indefinitely. Hard-close of the underlying conn always happens.
func (s *netconfSession) close() error {
	if s.rw == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		_, _ = s.rpc(`<close-session/>`)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		// Device isn't responding; the hard-close below will
		// unblock the goroutine by tearing down the conn.
	}
	err := s.rw.Close()
	s.rw = nil
	return err
}

// ---------------------------------------------------------------
// Hello marshaling / parsing
// ---------------------------------------------------------------

func buildHello(caps []string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<hello xmlns="`)
	b.WriteString(netconfBase10)
	b.WriteString(`"><capabilities>`)
	for _, c := range caps {
		b.WriteString(`<capability>`)
		xmlEscapeText(&b, c)
		b.WriteString(`</capability>`)
	}
	b.WriteString(`</capabilities></hello>`)
	return []byte(b.String())
}

type helloXML struct {
	XMLName      xml.Name `xml:"hello"`
	Capabilities struct {
		Capability []string `xml:"capability"`
	} `xml:"capabilities"`
	SessionID int `xml:"session-id"`
}

func parseHello(raw []byte) (map[string]struct{}, int, error) {
	var h helloXML
	if err := xml.Unmarshal(raw, &h); err != nil {
		return nil, 0, fmt.Errorf("unmarshal hello: %w", err)
	}
	out := make(map[string]struct{}, len(h.Capabilities.Capability))
	for _, c := range h.Capabilities.Capability {
		out[strings.TrimSpace(c)] = struct{}{}
	}
	return out, h.SessionID, nil
}

// ---------------------------------------------------------------
// RPC-reply parsing
// ---------------------------------------------------------------

type rpcReply struct {
	MessageID string
	Data      []byte       // innerXML of <data>, if present
	OKOnly    bool         // true when the reply was <ok/>
	Errors    []rpcError
}

type rpcError struct {
	Type     string `xml:"error-type"`
	Tag      string `xml:"error-tag"`
	Severity string `xml:"error-severity"`
	Message  string `xml:"error-message"`
	AppTag   string `xml:"error-app-tag"`
	Path     string `xml:"error-path"`
}

// parseRPCReply extracts the fields we care about. We avoid a
// full struct-unmarshal of every possible reply shape because
// Cisco YANG operational RPCs return arbitrary subtrees; Data
// is retained as raw bytes so callers can do their own parsing.
func parseRPCReply(raw []byte) (*rpcReply, error) {
	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	reply := &rpcReply{}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode rpc-reply: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local == "rpc-reply" {
			for _, attr := range start.Attr {
				if attr.Name.Local == "message-id" {
					reply.MessageID = attr.Value
				}
			}
			continue
		}
		switch start.Name.Local {
		case "ok":
			reply.OKOnly = true
			// Skip past the self-closing element.
			_ = dec.Skip()
		case "data":
			var inner struct {
				InnerXML []byte `xml:",innerxml"`
			}
			if err := dec.DecodeElement(&inner, &start); err != nil {
				return nil, fmt.Errorf("decode <data>: %w", err)
			}
			reply.Data = inner.InnerXML
		case "rpc-error":
			var e rpcError
			if err := dec.DecodeElement(&e, &start); err != nil {
				return nil, fmt.Errorf("decode <rpc-error>: %w", err)
			}
			reply.Errors = append(reply.Errors, e)
		}
	}
	return reply, nil
}

func formatRPCErrors(errs []rpcError) error {
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		parts = append(parts, fmt.Sprintf(
			"[%s/%s/%s] %s", e.Severity, e.Type, e.Tag, e.Message))
	}
	return fmt.Errorf("rpc-error: %s", strings.Join(parts, "; "))
}

// ---------------------------------------------------------------
// Minimal helpers
// ---------------------------------------------------------------

func indexOf(ss []string, needle string) (int, bool) {
	for i, s := range ss {
		if s == needle {
			return i, true
		}
	}
	return -1, false
}

// xmlEscapeText writes s into b with XML-escaping. The standard
// library's xml.EscapeText writes to an io.Writer; we use a
// strings.Builder path for allocation-light hello construction.
func xmlEscapeText(b *strings.Builder, s string) {
	for _, r := range s {
		switch r {
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			b.WriteString("&amp;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			b.WriteRune(r)
		}
	}
}
