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
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// GNMIConfig carries everything NewGNMI needs at construction time.
// Username/Password produce a gRPC metadata "authorization" header
// using HTTP Basic — the same auth IOS-XE's gNMI server accepts.
// Operators preferring per-call x.509 authentication wire ClientConn
// directly via Conn.
type GNMIConfig struct {
	Address string
	Port    int

	// TLSConfig, when non-nil, is used as the gRPC dial credentials.
	// Set to a zero-value tls.Config to use system TLS verification
	// against the device certificate. Empty (nil) and Conn==nil
	// switches to insecure plaintext, suitable for the integration-
	// test bufconn server. Production callers always supply this.
	TLSConfig *tls.Config

	Username string
	Password string

	// Conn, when non-nil, is reused verbatim — used by tests that
	// dial via bufconn and by integration runs that want to share
	// a single grpc.ClientConn across multiple transports.
	Conn *grpc.ClientConn
}

// NewGNMI constructs a gNMI transport. The dial happens lazily on
// the first RPC so a misconfigured Address fails at use time, not
// at NewGNMI time — matching the existing RESTCONF/NETCONF
// constructors.
func NewGNMI(cfg GNMIConfig) (Interface, error) {
	if cfg.Conn == nil && cfg.Address == "" {
		return nil, errors.New("transport.NewGNMI: Address empty and no Conn provided")
	}
	port := cfg.Port
	if port == 0 {
		port = 6030 // OpenConfig gNMI default; IOS-XE listens here too.
	}
	t := &gnmiTransport{
		cfg:    cfg,
		port:   port,
		conn:   cfg.Conn,
		closed: cfg.Conn != nil, // injected conn is the caller's; we do not close it.
	}
	return t, nil
}

// gnmiTransport implements transport.Interface against a gNMI
// (gRPC + protobuf) device. Atomicity within a transaction comes
// from gNMI's SetRequest — every op in a single SetRequest is
// applied as one device-side transaction.
type gnmiTransport struct {
	cfg     GNMIConfig
	port    int
	connMu  sync.Mutex
	conn    *grpc.ClientConn
	closed  bool
	keepCli bool

	// Pending ops accumulated under an open transaction. The
	// transport's contract is: Mutate during a transaction
	// appends; Commit flushes them as a single SetRequest;
	// Discard drops them.
	txMu   sync.Mutex
	open   bool
	tx     TxHandle
	queued []Op
}

func (t *gnmiTransport) Capabilities() Capabilities {
	return Capabilities{
		Kind:                 KindGNMI,
		SupportsTransactions: true,
		SupportsSubscribe:    false, // wired separately when Subscribe lands
		SupportsSaveStartup:  false, // gNMI has no save-config primitive
	}
}

// dial constructs a grpc.ClientConn against the configured target.
// Held under connMu so concurrent first-use callers share one conn.
func (t *gnmiTransport) dial(ctx context.Context) (gpb.GNMIClient, error) {
	t.connMu.Lock()
	defer t.connMu.Unlock()
	if t.conn != nil {
		return gpb.NewGNMIClient(t.conn), nil
	}
	target := fmt.Sprintf("%s:%d", t.cfg.Address, t.port)
	var creds credentials.TransportCredentials
	if t.cfg.TLSConfig != nil {
		creds = credentials.NewTLS(t.cfg.TLSConfig)
	} else {
		creds = insecure.NewCredentials()
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("gnmi dial %s: %w", target, err)
	}
	t.conn = conn
	t.keepCli = false
	return gpb.NewGNMIClient(conn), nil
}

// authCtx attaches HTTP-Basic credentials as gRPC metadata if the
// caller supplied them. IOS-XE's gNMI server accepts this shape;
// other vendors may need a different scheme — extend here when
// they show up.
func (t *gnmiTransport) authCtx(ctx context.Context) context.Context {
	if t.cfg.Username == "" {
		return ctx
	}
	creds := base64.StdEncoding.EncodeToString(
		[]byte(t.cfg.Username + ":" + t.cfg.Password))
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Basic "+creds)
}

// Fetch issues a gNMI Get against the path encoded as RESTCONF-
// style xpath. The response is the raw json_ietf-encoded value of
// the first update in the response — writers parse it the same
// way they would a RESTCONF JSON body.
func (t *gnmiTransport) Fetch(ctx context.Context, path string) ([]byte, error) {
	cli, err := t.dial(ctx)
	if err != nil {
		return nil, err
	}
	gpath, err := parseGNMIPath(path)
	if err != nil {
		return nil, fmt.Errorf("gnmi Fetch: %w", err)
	}
	resp, err := cli.Get(t.authCtx(ctx), &gpb.GetRequest{
		Path:     []*gpb.Path{gpath},
		Encoding: gpb.Encoding_JSON_IETF,
	})
	if err != nil {
		return nil, fmt.Errorf("gnmi Get %s: %w", path, err)
	}
	for _, n := range resp.GetNotification() {
		for _, u := range n.GetUpdate() {
			tv := u.GetVal()
			if tv == nil {
				continue
			}
			if v := tv.GetJsonIetfVal(); len(v) > 0 {
				return v, nil
			}
			if v := tv.GetJsonVal(); len(v) > 0 {
				return v, nil
			}
		}
	}
	return nil, nil
}

func (t *gnmiTransport) StartTransaction(ctx context.Context) (TxHandle, error) {
	t.txMu.Lock()
	defer t.txMu.Unlock()
	if t.open {
		return "", errors.New("gnmi: transaction already open")
	}
	t.open = true
	t.tx = TxHandle(fmt.Sprintf("gnmi-%p", t))
	t.queued = t.queued[:0]
	return t.tx, nil
}

// Mutate either accumulates ops under the active transaction or, if
// tx is the zero value, flushes them immediately as a single
// SetRequest. The latter matches the RESTCONF/NETCONF "apply now"
// semantics for non-transactional callers.
func (t *gnmiTransport) Mutate(ctx context.Context, tx TxHandle, ops []Op) error {
	if len(ops) == 0 {
		return nil
	}
	if tx == "" {
		return t.flush(ctx, ops)
	}
	t.txMu.Lock()
	defer t.txMu.Unlock()
	if !t.open || tx != t.tx {
		return fmt.Errorf("gnmi Mutate: transaction %q is not open", tx)
	}
	t.queued = append(t.queued, ops...)
	return nil
}

func (t *gnmiTransport) Commit(ctx context.Context, tx TxHandle) error {
	t.txMu.Lock()
	if !t.open || tx != t.tx {
		t.txMu.Unlock()
		return fmt.Errorf("gnmi Commit: transaction %q is not open", tx)
	}
	queued := t.queued
	t.queued = nil
	t.open = false
	t.tx = ""
	t.txMu.Unlock()
	if len(queued) == 0 {
		return nil
	}
	return t.flush(ctx, queued)
}

func (t *gnmiTransport) Discard(ctx context.Context, tx TxHandle) error {
	t.txMu.Lock()
	defer t.txMu.Unlock()
	if !t.open {
		return nil
	}
	if tx != t.tx {
		return fmt.Errorf("gnmi Discard: transaction %q is not open", tx)
	}
	t.queued = nil
	t.open = false
	t.tx = ""
	return nil
}

func (t *gnmiTransport) SaveStartup(ctx context.Context) error {
	// gNMI does not standardise a save-config RPC. Callers checking
	// Capabilities().SupportsSaveStartup get the right answer; this
	// method exists only because the interface mandates it.
	return ErrUnsupported
}

func (t *gnmiTransport) Close() error {
	t.connMu.Lock()
	defer t.connMu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	if t.conn == nil {
		return nil
	}
	return t.conn.Close()
}

// flush sends ops as a single SetRequest. gNMI Set is atomic: every
// update / replace / delete in one request lands together at the
// device, or the request fails as a whole. That gives us free
// transactional semantics for free under "tx == zero" callers and
// for the Commit path.
func (t *gnmiTransport) flush(ctx context.Context, ops []Op) error {
	cli, err := t.dial(ctx)
	if err != nil {
		return err
	}
	req := &gpb.SetRequest{}
	for i, op := range ops {
		gpath, err := parseGNMIPath(op.Path)
		if err != nil {
			return fmt.Errorf("op[%d]: %w", i, err)
		}
		switch op.Verb {
		case VerbReplace:
			req.Replace = append(req.Replace, &gpb.Update{
				Path: gpath,
				Val:  &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: op.Body}},
			})
		case VerbMerge:
			req.Update = append(req.Update, &gpb.Update{
				Path: gpath,
				Val:  &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: op.Body}},
			})
		case VerbDelete:
			req.Delete = append(req.Delete, gpath)
		case VerbCLI:
			return fmt.Errorf("op[%d]: CLI verb not supported on gNMI transport", i)
		default:
			return fmt.Errorf("op[%d]: unknown verb %q", i, op.Verb)
		}
	}
	if _, err := cli.Set(t.authCtx(ctx), req); err != nil {
		return fmt.Errorf("gnmi Set: %w", err)
	}
	return nil
}

// parseGNMIPath converts a RESTCONF-style xpath
// (`/Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list=10`)
// into a *gpb.Path. The conversion follows three rules:
//
//   - Each "/"-separated segment becomes one PathElem. Empty leading
//     segment (the path starts with "/") is dropped.
//   - "module:name" prefixes are stripped — gNMI carries module as
//     a dedicated field, but for IOS-XE round-tripping we keep
//     things simple and just use the local name. Devices that
//     enforce strict module-qualified paths can have this loosened
//     family-by-family later.
//   - "name=value" suffixes become single-key map entries with the
//     YANG list-key field. Multi-key lists are not yet supported;
//     they require schema knowledge and would force this parser to
//     consult families.yaml. RESTCONF paths in CVK use single keys
//     today, so the limitation is theoretical.
func parseGNMIPath(p string) (*gpb.Path, error) {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return &gpb.Path{}, nil
	}
	p = strings.TrimPrefix(p, "/")
	out := &gpb.Path{}
	for _, raw := range strings.Split(p, "/") {
		if raw == "" {
			continue
		}
		seg, key := raw, ""
		if i := strings.Index(raw, "="); i > 0 {
			seg, key = raw[:i], raw[i+1:]
		}
		if i := strings.Index(seg, ":"); i > 0 {
			seg = seg[i+1:]
		}
		elem := &gpb.PathElem{Name: seg}
		if key != "" {
			// Single-key list; the YANG schema decides which leaf is
			// the key. We surface it under "name" with a numeric
			// fallback — RESTCONF list-keys are textual, but YANG
			// list-keys can be either, and "name" is by far the most
			// common in IOS-XE schemas.
			keyName := "name"
			if _, err := strconv.Atoi(key); err == nil {
				keyName = "id"
			}
			elem.Key = map[string]string{keyName: key}
		}
		out.Elem = append(out.Elem, elem)
	}
	return out, nil
}
