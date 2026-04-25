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
		SupportsSubscribe:    true,
		SupportsSaveStartup:  false, // gNMI has no save-config primitive
	}
}

// Subscribe opens a STREAM-mode subscription against the device for
// every path in paths. The returned channel closes when ctx
// cancels, the device closes the stream, or a transport error
// surfaces. Errors are delivered as a final SubscribeEvent with
// Err set so the consumer can react without a separate signalling
// channel.
//
// Mode maps directly to gNMI's SubscriptionMode. The caller picks
// ON_CHANGE for drift detection; SAMPLE is reserved for operational
// state monitoring.
//
// The buffered channel (16 entries) accommodates moderate burst
// without coupling consumer latency to the device. A slow consumer
// that overflows the buffer is the consumer's bug — the watcher
// drops events on the floor and bumps a metric so it's visible
// rather than silently stalling the device-side stream.
func (t *gnmiTransport) Subscribe(ctx context.Context, paths []string, mode SubscribeMode) (<-chan SubscribeEvent, error) {
	cli, err := t.dial(ctx)
	if err != nil {
		return nil, err
	}
	subs := make([]*gpb.Subscription, 0, len(paths))
	for _, p := range paths {
		gpath, err := parseGNMIPath(p)
		if err != nil {
			return nil, fmt.Errorf("subscribe path %q: %w", p, err)
		}
		subs = append(subs, &gpb.Subscription{
			Path: gpath,
			Mode: subscriptionMode(mode),
		})
	}

	stream, err := cli.Subscribe(t.authCtx(ctx))
	if err != nil {
		return nil, fmt.Errorf("gnmi Subscribe open: %w", err)
	}
	if err := stream.Send(&gpb.SubscribeRequest{
		Request: &gpb.SubscribeRequest_Subscribe{
			Subscribe: &gpb.SubscriptionList{
				Subscription: subs,
				Encoding:     gpb.Encoding_JSON_IETF,
				Mode:         gpb.SubscriptionList_STREAM,
			},
		},
	}); err != nil {
		return nil, fmt.Errorf("gnmi Subscribe send: %w", err)
	}

	out := make(chan SubscribeEvent, 16)
	go t.pumpSubscribe(ctx, stream, out)
	return out, nil
}

// pumpSubscribe is the goroutine that turns a gNMI stream into
// SubscribeEvent values. Lives off the public API so tests of the
// channel semantics can substitute their own producer.
func (t *gnmiTransport) pumpSubscribe(
	ctx context.Context,
	stream gpb.GNMI_SubscribeClient,
	out chan<- SubscribeEvent,
) {
	defer close(out)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		resp, err := stream.Recv()
		if err != nil {
			// io.EOF on a STREAM-mode subscription means the device
			// closed cleanly — that's still a transport-level
			// signal the consumer needs to know about so it can
			// fall back to polling.
			out <- SubscribeEvent{Err: fmt.Errorf("gnmi Recv: %w", err)}
			return
		}
		notif := resp.GetUpdate()
		if notif == nil {
			continue
		}
		// Each notification carries up to N updates and N deletes.
		// We flatten to one event per leaf.
		for _, u := range notif.GetUpdate() {
			ev := SubscribeEvent{Path: pathToString(u.Path)}
			if v := u.GetVal(); v != nil {
				if b := v.GetJsonIetfVal(); len(b) > 0 {
					ev.Value = b
				} else if b := v.GetJsonVal(); len(b) > 0 {
					ev.Value = b
				}
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			default:
				// Drop on overflow — better than back-pressuring
				// the device-side stream. The drop is observable
				// via cisco_vk_config_subscribe_events_dropped_total;
				// alert on rate, not absolute value.
				recordSubscribeDropped(t.cfg.Address)
			}
		}
		for _, p := range notif.GetDelete() {
			ev := SubscribeEvent{Path: pathToString(p), Delete: true}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			default:
				recordSubscribeDropped(t.cfg.Address)
			}
		}
	}
}

func subscriptionMode(m SubscribeMode) gpb.SubscriptionMode {
	switch m {
	case SubscribeOnChange:
		return gpb.SubscriptionMode_ON_CHANGE
	case SubscribeSample:
		return gpb.SubscriptionMode_SAMPLE
	default:
		return gpb.SubscriptionMode_TARGET_DEFINED
	}
}

// pathToString reverses parseGNMIPath enough that the consumer can
// display a recognisable path. Module prefixes are dropped on the
// way in, so the round-trip isn't byte-equal — this is a
// best-effort reconstruction for logging, not for re-parsing.
func pathToString(p *gpb.Path) string {
	if p == nil {
		return ""
	}
	var b strings.Builder
	for _, e := range p.Elem {
		b.WriteByte('/')
		b.WriteString(e.Name)
		for k, v := range e.Key {
			b.WriteByte('[')
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(v)
			b.WriteByte(']')
		}
	}
	return b.String()
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
			// Wave 5A: prefer the schema-registered key field over
			// the value-type heuristic. RegisterPathKey is called by
			// the schema layer at startup, once per keyed-list family
			// in families.yaml. Lists keyed by `tag`, `seq`, `prefix`,
			// `sequence`, etc. (and not by `name` or `id`) now produce
			// gNMI Set/Delete paths that actually match a list entry
			// on the device. When the registry has no entry — lint-
			// tool offline mode, or a family the schema layer hasn't
			// initialised — we fall back to the historical name/id
			// heuristic so legacy callers stay correct.
			keyName := pathKeyFor(seg)
			if keyName == "" {
				keyName = "name"
				if _, err := strconv.Atoi(key); err == nil {
					keyName = "id"
				}
			}
			elem.Key = map[string]string{keyName: key}
		}
		out.Elem = append(out.Elem, elem)
	}
	return out, nil
}
