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
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// NETCONFConfig configures a NETCONF transport. Produces a live
// transport when either Conn (injected pipe, for tests) or Address
// (SSH dialer, for production) is supplied; SSHConfig overrides the
// dialer's defaults for auth.
type NETCONFConfig struct {
	// Address is the device host. Ignored when Conn is set.
	Address string
	// Port defaults to 830 (IANA NETCONF-over-SSH).
	Port int

	// Username / Password for SSH auth. Password is required when
	// HostKeyCallback does not pin a key (HostKeyCallback nil means
	// "accept any", matching the Phase-1 RESTCONF path's
	// --insecure flag semantics).
	Username string
	Password string

	// HostKeyCallback is passed through to the SSH client. When nil
	// we default to ssh.InsecureIgnoreHostKey(), suitable for lab
	// use; production deployments MUST pin a key via this field.
	HostKeyCallback ssh.HostKeyCallback

	// Timeout bounds the SSH dial. The RPC layer itself does not
	// currently enforce per-call timeouts; the context passed to
	// Fetch / Mutate / Commit controls cancellation on the server
	// side only (close the session to abort).
	Timeout time.Duration

	// Conn, when non-nil, is used verbatim instead of dialing SSH.
	// Intended for tests that run the NETCONF RPC layer against a
	// pipe or an in-process mock server.
	Conn io.ReadWriteCloser
}

// netconfTransport implements transport.Interface by running
// NETCONF RPCs over the session. RPC ordering within a single
// transport is serialised by the session's own mutex; multiple
// transports against the same device are independent sessions.
type netconfTransport struct {
	cfg     NETCONFConfig
	session *netconfSession

	// caps reported via Capabilities(). Populated on first call
	// from session.serverCaps.
	capsOnce sync.Once
	caps     Capabilities
}

// clientCapabilities is the list we advertise in hello. base:1.0
// is mandatory; base:1.1 is advertised but we default to 1.0
// framing in practice because it keeps the test-path simple.
//
// Wave 10 — confirmed-commit:1.0 is advertised so the device knows
// we're prepared to participate in the auto-revert flow if it also
// supports it. Capability *advertisement* is one-way: a device that
// doesn't advertise the matching capability still works with us via
// the engine's plain-Commit fallback. We're saying "we can do this
// if you can," not "you must."
var clientCapabilities = []string{
	capBase10,
	capConfirm,
}

// NewNETCONF builds a NETCONF transport. Either cfg.Conn or
// cfg.Address must be set.
func NewNETCONF(cfg NETCONFConfig) (Interface, error) {
	if cfg.Conn == nil && cfg.Address == "" {
		return nil, fmt.Errorf("NETCONF: either Address or Conn must be set")
	}

	conn := cfg.Conn
	if conn == nil {
		c, err := dialSSHNetconf(cfg)
		if err != nil {
			return nil, fmt.Errorf("NETCONF: dial: %w", err)
		}
		conn = c
	}

	sess := newNetconfSession(conn)
	if err := sess.hello(clientCapabilities); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("NETCONF: hello: %w", err)
	}

	return &netconfTransport{cfg: cfg, session: sess}, nil
}

// Capabilities reports what the server advertised during hello.
// Computed once and cached — a hello happens only at startup.
func (t *netconfTransport) Capabilities() Capabilities {
	t.capsOnce.Do(func() {
		_, hasCand := t.session.serverCaps[capCand]
		// Wave 10 — confirmed-commit:1.0 is the RFC 6241 §8.4
		// auto-revert capability the engine consults to decide
		// whether to use the CommitConfirmed → ConfirmCommit flow
		// or fall back to plain Commit. Older IOS-XE images that
		// don't advertise it transparently fall back; the
		// per-CR spec.confirmTimeoutSeconds knob is honored only
		// when both this capability is true AND the engine's
		// type-assertion against ConfirmedCommitter succeeds.
		_, hasConfirm := t.session.serverCaps[capConfirm]
		t.caps = Capabilities{
			Kind:                    KindNETCONF,
			SupportsTransactions:    hasCand,
			SupportsSaveStartup:     true,  // Cisco-IA RPC covers this
			SupportsSubscribe:       false, // RFC 5277 not wired
			SupportsConfirmedCommit: hasConfirm,
		}
	})
	return t.caps
}

// Fetch runs <get-config source=running filter=subtree(path)> and
// returns the response in yang-data+json shape so family writers
// can decode it. path is the RESTCONF path format the writers use
// (e.g. /Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list);
// pathToSubtreeFilter converts it to the NETCONF subtree form.
func (t *netconfTransport) Fetch(ctx context.Context, path string) ([]byte, error) {
	return t.fetchFromSource(path, "running")
}

// FetchTx implements TxFetcher: read the candidate datastore so
// the engine's verify-Fetch sees the writes the in-flight
// transaction just made. The handle is "candidate" today; any
// other value falls back to running so a stray nil-handle Fetch
// does the safe thing.
//
// Wave 1A-fu: this is the load-bearing wiring for transactional
// commit correctness. Without it the verify-Diff reads stale
// running and reports drift, the engine Discard's the candidate,
// and the apply that would have committed cleanly never does.
func (t *netconfTransport) FetchTx(ctx context.Context, tx TxHandle, path string) ([]byte, error) {
	source := "running"
	if tx == "candidate" {
		source = "candidate"
	}
	return t.fetchFromSource(path, source)
}

// fetchFromSource is the shared Fetch implementation parameterised
// on the NETCONF datastore source ("running" or "candidate").
// Splitting Fetch out this way keeps the legacy contract intact for
// non-transactional callers while letting FetchTx target candidate.
func (t *netconfTransport) fetchFromSource(path, source string) ([]byte, error) {
	filter, err := pathToSubtreeFilter(path)
	if err != nil {
		return nil, fmt.Errorf("NETCONF Fetch: %w", err)
	}
	inner := fmt.Sprintf(
		`<get-config><source><%s/></source><filter type="subtree">%s</filter></get-config>`,
		source, filter)
	reply, err := t.session.rpc(inner)
	if err != nil {
		return nil, err
	}
	jsonBytes, err := xmlToYangJSON(reply.Data, ciscoYANGPrefixes)
	if err != nil {
		return nil, fmt.Errorf("NETCONF Fetch: convert xml: %w", err)
	}
	return jsonBytes, nil
}

// StartTransaction locks the candidate datastore. A TxHandle of
// "candidate" flags that subsequent Mutate calls should target
// the candidate datastore and should be followed by Commit.
//
// Returns ErrUnsupported when the server did not advertise the
// candidate capability — Cisco IOS-XE devices almost always do.
func (t *netconfTransport) StartTransaction(ctx context.Context) (TxHandle, error) {
	if !t.Capabilities().SupportsTransactions {
		return "", ErrUnsupported
	}
	if _, err := t.session.rpc(`<lock><target><candidate/></target></lock>`); err != nil {
		return "", fmt.Errorf("NETCONF StartTransaction: lock: %w", err)
	}
	return TxHandle("candidate"), nil
}

// Mutate applies ops in order. When tx is "candidate" each op is
// an <edit-config target=candidate>; when tx is empty each op is
// an <edit-config target=running>.
func (t *netconfTransport) Mutate(ctx context.Context, tx TxHandle, ops []Op) error {
	target := "running"
	if tx == "candidate" {
		target = "candidate"
	}
	for i, op := range ops {
		if op.Verb == VerbCLI {
			if err := t.pushCLI(op.Body); err != nil {
				return fmt.Errorf("op[%d] CLI: %w", i, err)
			}
			continue
		}
		edit, err := editConfigXML(target, op)
		if err != nil {
			return fmt.Errorf("op[%d] %s %s: build edit-config: %w",
				i, op.Verb, op.Path, err)
		}
		if _, err := t.session.rpc(edit); err != nil {
			return fmt.Errorf("op[%d] %s %s: %w", i, op.Verb, op.Path, err)
		}
	}
	return nil
}

// Commit finalises a candidate-datastore transaction and releases
// the lock. A zero TxHandle is a no-op (matches RESTCONF's
// non-transactional behaviour).
func (t *netconfTransport) Commit(ctx context.Context, tx TxHandle) error {
	if tx == "" {
		return nil
	}
	if tx != "candidate" {
		return fmt.Errorf("NETCONF Commit: unknown TxHandle %q", tx)
	}
	if _, err := t.session.rpc(`<commit/>`); err != nil {
		return fmt.Errorf("NETCONF Commit: %w", err)
	}
	if _, err := t.session.rpc(`<unlock><target><candidate/></target></unlock>`); err != nil {
		return fmt.Errorf("NETCONF Commit: unlock: %w", err)
	}
	return nil
}

// CommitConfirmed implements ConfirmedCommitter. It issues a
// tentative commit with an RFC 6241 §8.4 auto-revert timer. The
// candidate datastore merges into running, but the device starts
// the timer; if ConfirmCommit is not called within `timeout` the
// device reverts running to its pre-commit state.
//
// Wave 10 risk-reduction primitive. Use case: the engine emits a
// risky change (ACL on management interface, BGP reconfiguration,
// IP-domain change). The change applies tentatively; the engine
// runs a post-commit Verify against running. If Verify succeeds
// → ConfirmCommit. If Verify fails OR the controller's session
// drops before Verify completes → device auto-reverts at timeout.
//
// Implementation notes:
//   - timeout is clamped at the transport boundary: minimum 1s
//     (NETCONF requires a positive integer), maximum 600s (the
//     engine's per-CR knob is capped at 300 by kubebuilder, but
//     a defensive transport-side ceiling protects against future
//     callers that don't go through that schema).
//   - The candidate-datastore lock is NOT released here. The lock
//     is released by ConfirmCommit (success path) or by Discard
//     (failure path triggered by the engine's deferred cleanup
//     when ConfirmCommit doesn't fire). This mirrors the existing
//     plain-Commit pattern and lets the engine's deferred
//     Discard semantics work unchanged on the auto-revert path.
//   - Running this against a server that did not advertise
//     confirmed-commit:1.0 returns the server's <rpc-error>
//     bubbled up as an error. The engine guards against this by
//     consulting Capabilities.SupportsConfirmedCommit before
//     dispatching, but the transport-level guard is here for
//     defense-in-depth.
func (t *netconfTransport) CommitConfirmed(ctx context.Context, tx TxHandle, timeout time.Duration) error {
	if tx == "" {
		return nil
	}
	if tx != "candidate" {
		return fmt.Errorf("NETCONF CommitConfirmed: unknown TxHandle %q", tx)
	}
	if !t.Capabilities().SupportsConfirmedCommit {
		return fmt.Errorf("NETCONF CommitConfirmed: server did not advertise %s", capConfirm)
	}
	secs := int(timeout / time.Second)
	if secs < 1 {
		secs = 1
	}
	if secs > 600 {
		secs = 600
	}
	rpc := fmt.Sprintf(`<commit><confirmed/><confirm-timeout>%d</confirm-timeout></commit>`, secs)
	if _, err := t.session.rpc(rpc); err != nil {
		return fmt.Errorf("NETCONF CommitConfirmed: %w", err)
	}
	return nil
}

// ConfirmCommit implements ConfirmedCommitter. It cancels the
// auto-revert timer started by CommitConfirmed and makes the
// tentative commit permanent. This is the "we verified the change
// works against running" signal.
//
// Wire-level: a plain <commit/> RPC. Per RFC 6241 §8.4, the
// server treats a plain commit during a pending confirm-timeout
// as the confirmation; no <confirmed/> element is needed.
//
// The candidate-datastore lock IS released here (mirrors plain
// Commit). After ConfirmCommit returns success, the engine
// considers the transaction complete; no Discard runs.
func (t *netconfTransport) ConfirmCommit(ctx context.Context) error {
	if _, err := t.session.rpc(`<commit/>`); err != nil {
		return fmt.Errorf("NETCONF ConfirmCommit: %w", err)
	}
	if _, err := t.session.rpc(`<unlock><target><candidate/></target></unlock>`); err != nil {
		// Unlock failure is non-fatal — the commit itself
		// succeeded and the lock will be released when the
		// session ends. Surface as a soft warning rather than
		// failing the whole reconcile.
		return fmt.Errorf("NETCONF ConfirmCommit: unlock: %w", err)
	}
	return nil
}

// Discard cancels a candidate transaction. Safe to call after a
// failed Mutate; the candidate is reset and the lock is released.
func (t *netconfTransport) Discard(ctx context.Context, tx TxHandle) error {
	if tx == "" {
		return nil
	}
	if _, err := t.session.rpc(`<discard-changes/>`); err != nil {
		return fmt.Errorf("NETCONF Discard: %w", err)
	}
	if _, err := t.session.rpc(`<unlock><target><candidate/></target></unlock>`); err != nil {
		return fmt.Errorf("NETCONF Discard: unlock: %w", err)
	}
	return nil
}

// SaveStartup uses the Cisco-IA save-config RPC, matching RESTCONF's
// behaviour so the capability reads the same on both transports.
func (t *netconfTransport) SaveStartup(ctx context.Context) error {
	_, err := t.session.rpc(`<save-config xmlns="http://cisco.com/yang/cisco-ia"/>`)
	return err
}

// Close sends <close-session/> and terminates the underlying
// transport. Idempotent.
func (t *netconfTransport) Close() error {
	if t.session == nil {
		return nil
	}
	return t.session.close()
}

// pushCLI invokes the Cisco-IA cli-config-data RPC with body as
// newline-separated CLI commands. Each line becomes a <cmd>. This
// is the NETCONF side of the CLI template render path (feedback 3b).
func (t *netconfTransport) pushCLI(body []byte) error {
	var lines []string
	switch {
	case len(body) == 0:
		return nil
	default:
		// body may be JSON-encoded (from shared CLI op building)
		// or a raw newline-delimited string. Detect JSON first.
		var asString string
		if err := json.Unmarshal(body, &asString); err == nil {
			lines = splitCLILines(asString)
		} else {
			lines = splitCLILines(string(body))
		}
	}
	if len(lines) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString(`<cli-config-data xmlns="http://cisco.com/yang/cisco-ia">`)
	for _, line := range lines {
		b.WriteString(`<cmd>`)
		xmlEscapeText(&b, line)
		b.WriteString(`</cmd>`)
	}
	b.WriteString(`</cli-config-data>`)
	_, err := t.session.rpc(b.String())
	return err
}

// splitCLILines normalises a CLI body into individual commands,
// trimming whitespace and dropping empty lines. Matches the
// formatting the Cisco-IA RPC expects — one <cmd> per CLI line.
func splitCLILines(s string) []string {
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		out = append(out, t)
	}
	return out
}

// pathToSubtreeFilter converts a RESTCONF-style path like
// /Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list
// into a nested subtree filter:
//
//	<native xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-native">
//	  <vlan>
//	    <vlan-list xmlns="http://cisco.com/ns/yang/Cisco-IOS-XE-vlan"/>
//	  </vlan>
//	</native>
//
// Namespaces are derived from the module-name prefix; the
// ciscoYANGPrefixes table in netconf_xml2json.go holds the
// forward mapping and we invert it here.
func pathToSubtreeFilter(path string) (string, error) {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	parts := strings.Split(path, "/")
	var b strings.Builder
	prefixToNS := invertPrefixMap(ciscoYANGPrefixes)
	for i, p := range parts {
		elem, mod := splitQualifiedName(p)
		b.WriteString("<")
		b.WriteString(elem)
		if mod != "" {
			if ns, ok := prefixToNS[mod]; ok {
				fmt.Fprintf(&b, ` xmlns="%s"`, ns)
			}
		}
		if i < len(parts)-1 {
			b.WriteString(">")
		} else {
			b.WriteString("/>")
		}
	}
	for i := len(parts) - 2; i >= 0; i-- {
		elem, _ := splitQualifiedName(parts[i])
		fmt.Fprintf(&b, "</%s>", elem)
	}
	return b.String(), nil
}

// splitQualifiedName parses "<module>:<local>" or "<local>" into
// its parts. RESTCONF paths carry the module only at namespace
// transitions, so most parts have no module component.
func splitQualifiedName(p string) (local, module string) {
	if idx := strings.Index(p, ":"); idx >= 0 {
		return p[idx+1:], p[:idx]
	}
	return p, ""
}

func invertPrefixMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[v] = k
	}
	return out
}

// editConfigXML builds an <edit-config> RPC body for one op.
// Verbs:
//
//   - VerbMerge   → operation="merge" (default edit-config behaviour)
//   - VerbReplace → operation="replace"
//   - VerbDelete  → operation="delete"
//
// VerbCLI is handled separately in Mutate (Cisco-IA RPC, not
// edit-config).
func editConfigXML(target string, op Op) (string, error) {
	var ncOp string
	switch op.Verb {
	case VerbMerge:
		ncOp = "merge"
	case VerbReplace:
		ncOp = "replace"
	case VerbDelete:
		ncOp = "delete"
	case VerbCLI:
		return "", fmt.Errorf("editConfigXML: VerbCLI is not edit-config")
	default:
		return "", fmt.Errorf("unknown verb %q", op.Verb)
	}
	subtree, err := pathToSubtreeFilterWithBody(op.Path, op.Body, ncOp)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		`<edit-config><target><%s/></target><config>%s</config></edit-config>`,
		target, subtree), nil
}

// pathToSubtreeFilterWithBody wraps pathToSubtreeFilter and
// injects the RESTCONF JSON body as a simplified XML payload on
// the leaf element. The Phase-1 approach: treat the body as
// opaque — it is passed through as inner XML after a best-effort
// JSON→XML shim. Families whose writer emits well-formed XML
// from their body are untouched; families that emit JSON round
// through jsonToNaiveXML below.
//
// This is an intentional simplification. Production NETCONF
// writers would marshal directly to XML in their Apply paths;
// Phase-1 keeps the writers transport-agnostic and pays the
// conversion cost in the transport layer. Families whose
// payload shape isn't covered cleanly by jsonToNaiveXML are
// tracked in docs/rfcs/iosxe-config-driver-review.md §10.
func pathToSubtreeFilterWithBody(path string, body []byte, ncOp string) (string, error) {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	parts := strings.Split(path, "/")
	var b strings.Builder
	prefixToNS := invertPrefixMap(ciscoYANGPrefixes)

	// Open nested elements up to the last part.
	for i, p := range parts {
		elem, mod := splitQualifiedName(p)
		b.WriteString("<")
		b.WriteString(elem)
		if mod != "" {
			if ns, ok := prefixToNS[mod]; ok {
				fmt.Fprintf(&b, ` xmlns="%s"`, ns)
			}
		}
		// Place the operation attribute on the innermost element
		// only; inner elements inherit unless explicitly
		// overridden, which keeps the XML small and readable.
		if i == len(parts)-1 {
			fmt.Fprintf(&b, ` xmlns:nc="%s" nc:operation="%s"`, netconfBase10, ncOp)
		}
		b.WriteString(">")

		// Inject the body on the last part.
		if i == len(parts)-1 && len(body) > 0 {
			inner, err := jsonToNaiveXML(body)
			if err != nil {
				return "", fmt.Errorf("body → xml: %w", err)
			}
			b.WriteString(inner)
		}
	}
	for i := len(parts) - 1; i >= 0; i-- {
		elem, _ := splitQualifiedName(parts[i])
		fmt.Fprintf(&b, "</%s>", elem)
	}
	return b.String(), nil
}

// jsonToNaiveXML is a minimal JSON → XML converter for NETCONF
// edit-config payloads. Handles the shapes Phase-1 writers
// produce:
//
//   - {"key": "scalar"}         → <key>scalar</key>
//   - {"key": [a, b]}            → <key>...</key><key>...</key>
//   - {"key": {...}}             → <key>...</key>
//   - {"module:key": ...}        → strips the module prefix (the
//     edit-config namespace is on the outer element already).
//
// This is an intentional simplification; see
// pathToSubtreeFilterWithBody for the caveat.
func jsonToNaiveXML(body []byte) (string, error) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return "", err
	}
	var b strings.Builder
	if err := emitXML(&b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}

func emitXML(b *strings.Builder, v any) error {
	switch tv := v.(type) {
	case map[string]any:
		for k, val := range tv {
			localKey := k
			if idx := strings.Index(k, ":"); idx >= 0 {
				localKey = k[idx+1:]
			}
			switch inner := val.(type) {
			case []any:
				for _, el := range inner {
					fmt.Fprintf(b, "<%s>", localKey)
					if err := emitXML(b, el); err != nil {
						return err
					}
					fmt.Fprintf(b, "</%s>", localKey)
				}
			default:
				fmt.Fprintf(b, "<%s>", localKey)
				if err := emitXML(b, val); err != nil {
					return err
				}
				fmt.Fprintf(b, "</%s>", localKey)
			}
		}
	case []any:
		for _, el := range tv {
			if err := emitXML(b, el); err != nil {
				return err
			}
		}
	case nil:
		// Empty element.
	case string:
		xmlEscapeText(b, tv)
	case bool:
		if tv {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	default:
		fmt.Fprintf(b, "%v", v)
	}
	return nil
}

// ---------------------------------------------------------------
// SSH dialer (production path)
// ---------------------------------------------------------------

// sshChannelConn adapts an ssh.Session to io.ReadWriteCloser by
// piping stdin/stdout through the subsystem. Close terminates
// both halves.
type sshChannelConn struct {
	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
}

func (c *sshChannelConn) Read(p []byte) (int, error)  { return c.stdout.Read(p) }
func (c *sshChannelConn) Write(p []byte) (int, error) { return c.stdin.Write(p) }

func (c *sshChannelConn) Close() error {
	// stdin.Close flushes + signals EOF to the remote netconf
	// subsystem so it exits cleanly.
	_ = c.stdin.Close()
	// Wait for the subsystem to exit; ignore errors — we're
	// closing anyway.
	_ = c.session.Wait()
	_ = c.session.Close()
	return c.client.Close()
}

func dialSSHNetconf(cfg NETCONFConfig) (io.ReadWriteCloser, error) {
	port := cfg.Port
	if port == 0 {
		port = 830
	}
	if cfg.HostKeyCallback == nil {
		cfg.HostKeyCallback = ssh.InsecureIgnoreHostKey()
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	conf := &ssh.ClientConfig{
		User:            cfg.Username,
		Auth:            []ssh.AuthMethod{ssh.Password(cfg.Password)},
		HostKeyCallback: cfg.HostKeyCallback,
		Timeout:         timeout,
		// TLS config hint for hosts that also proxy the SSH
		// handshake — not typical, but a harmless default.
		BannerCallback: func(message string) error { return nil },
	}
	_ = tls.Config{} // keep crypto/tls imported for the future SSH-over-TLS bridge

	addr := fmt.Sprintf("%s:%d", cfg.Address, port)
	client, err := ssh.Dial("tcp", addr, conf)
	if err != nil {
		return nil, err
	}
	session, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, err
	}
	if err := session.RequestSubsystem("netconf"); err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, fmt.Errorf("request netconf subsystem: %w", err)
	}
	return &sshChannelConn{
		client: client, session: session,
		stdin: stdin, stdout: stdout,
	}, nil
}
