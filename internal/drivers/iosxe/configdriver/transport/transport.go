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

// Package transport provides the pluggable device-facing channel the
// config driver uses. One implementation (RESTCONF) ships in Phase 1;
// NETCONF and gNMI implementations slot in behind the same Interface.
//
// Implementations MUST serialise requests against any shared underlying
// session (e.g. the apphosting driver's HTTP client) so a concurrent
// apphosting write cannot interleave with a configuration write and
// corrupt the device's transaction state.
package transport

import (
	"context"
	"errors"
)

// Kind identifies a transport flavour. The enum is closed so the factory
// and capability report can be table-driven.
//
// +kubebuilder:validation:Enum=restconf;netconf;gnmi
type Kind string

// Supported transport kinds.
const (
	KindRESTCONF Kind = "restconf"
	KindNETCONF  Kind = "netconf"
	KindGNMI     Kind = "gnmi"
)

// Capabilities describes what a concrete transport can do, so generic
// driver code (engine, writers) can make informed decisions without
// sniffing the implementation type.
//
// Non-trivial invariants expressed here:
//
//   - SupportsTransactions implies Mutate calls that share a TxHandle
//     are atomic at the device. RESTCONF, with no candidate datastore,
//     sets this to false.
//   - SupportsSubscribe flags push-based drift detection (gNMI SAMPLE/ON_CHANGE).
type Capabilities struct {
	Kind                 Kind
	SupportsTransactions bool
	SupportsSubscribe    bool
	SupportsSaveStartup  bool
}

// Op describes a single device-facing mutation at the transport layer.
// Writers build Op slices; the transport translates each Op to the
// underlying protocol (RESTCONF verb, NETCONF edit-config element, or
// gNMI SetRequest update).
type Op struct {
	// Verb is one of Replace, Merge, Delete. These map cleanly:
	//   - RESTCONF: Replace=PUT, Merge=PATCH, Delete=DELETE.
	//   - NETCONF:  edit-config operation attribute.
	//   - gNMI:     Replace/Update/Delete in SetRequest.
	Verb Verb
	// Path is a YANG xpath addressing the resource being mutated. The
	// transport adapter converts to protocol-specific path representation.
	Path string
	// Body is the JSON (RESTCONF) or gNMI.TypedValue-serialised (gNMI)
	// payload. Nil for Delete.
	Body []byte
}

// Verb enumerates the small set of mutation shapes every transport must
// express. Keeping this closed means writers can be transport-agnostic.
type Verb string

// Supported verbs.
const (
	VerbReplace Verb = "REPLACE"
	VerbMerge   Verb = "MERGE"
	VerbDelete  Verb = "DELETE"
	// VerbCLI pushes an IOS-XE CLI block through the device's
	// Cisco-IA cli-config-data RPC. Body is a newline-delimited
	// CLI-text payload (or a JSON string of that payload); Path is
	// ignored by the transport — the RPC endpoint is fixed. CLI
	// templates (IOSXETemplate.spec.type=cli) produce ops with
	// this verb during resolution. Both RESTCONF and NETCONF
	// transports implement it.
	VerbCLI Verb = "CLI"
)

// SubscribeMode picks the cadence the device reports updates at.
// gNMI maps these directly to its SAMPLE / ON_CHANGE submodes;
// other protocols translate at the adapter layer (NETCONF YANG-Push
// is a future Phase-6.5 add).
type SubscribeMode string

const (
	// SubscribeOnChange asks the device to emit one notification per
	// path mutation. The natural choice for drift detection: a write
	// outside CVK's apply path triggers an event the engine can use
	// to fast-path a reconcile.
	SubscribeOnChange SubscribeMode = "ON_CHANGE"

	// SubscribeSample asks the device to emit periodic snapshots of
	// the path value, regardless of whether it changed. Useful when
	// the underlying YANG element doesn't support change notifications
	// (operational state, counters); rarely the right pick for drift.
	SubscribeSample SubscribeMode = "SAMPLE"
)

// SubscribeEvent is one notification from the device about a path
// the consumer is watching. Path and Value are the wire shape of
// gNMI Update; Delete is true when the device reported the path
// being removed. Err carries any stream-level transport failure —
// the consumer treats a non-nil Err as the end of the stream and
// must fall back to polling.
type SubscribeEvent struct {
	Path   string
	Value  []byte // JSON_IETF for gNMI; protocol-specific bytes otherwise
	Delete bool
	Err    error
}

// SubscribeCapable is the optional interface a transport
// implements when it can stream change notifications. The engine
// consults this from a separate drift-watcher goroutine; transports
// that don't implement it stay on the existing periodic
// Fetch-diff loop. Mirrors the PruneCapable pattern.
type SubscribeCapable interface {
	// Subscribe opens a stream of SubscribeEvent values for paths
	// and mode. The returned channel is closed when ctx cancels or
	// the device-side stream ends. Implementations buffer modestly
	// (16 entries today) so a slow consumer doesn't propagate
	// back-pressure to the device.
	Subscribe(ctx context.Context, paths []string, mode SubscribeMode) (<-chan SubscribeEvent, error)
}

// TxFetcher is the optional interface a transport implements when
// it can read from a specific transaction's working datastore
// (NETCONF candidate, gNMI staged buffer, etc.) rather than the
// committed running config. The engine's verify-Fetch path consults
// this when a transaction is open: reading from the candidate sees
// the writes the transaction just made, so the verify-Diff
// correctly reports "applied as intended". Transports without a
// working-datastore concept (RESTCONF — running is the only
// surface) simply don't implement this and the engine falls back
// to plain Fetch.
//
// Wave 1A-fu (external-review-followup Finding #1). Before this
// interface existed, the transactionalView wrapper called
// Fetch(running) during verify, saw the pre-write state, reported
// residual drift, and Discard'd the candidate — transactional
// applies could not reliably reach Commit even when the writes
// would have committed cleanly.
type TxFetcher interface {
	// FetchTx returns the raw response at path read from the
	// transaction's working datastore identified by tx. tx is the
	// handle returned by StartTransaction. Encoding is protocol-
	// specific (XML for NETCONF) — same contract as Fetch.
	FetchTx(ctx context.Context, tx TxHandle, path string) ([]byte, error)
}

// TxHandle is an opaque per-transaction value returned by
// StartTransaction. Transports that do not support transactions return
// a zero-valued handle.
type TxHandle string

// Interface is the capability-aware transport contract. Methods that
// are not supported by a concrete implementation must return
// ErrUnsupported rather than silently no-op.
type Interface interface {
	// Capabilities reports the feature set the transport supports. The
	// value is stable for the lifetime of the connection; callers may
	// cache it.
	Capabilities() Capabilities

	// Fetch returns the raw response at path. The encoding is protocol-
	// specific — RESTCONF returns JSON, NETCONF returns XML, gNMI returns
	// serialised TypedValue bytes. Writers must know which transport
	// they are using before parsing.
	Fetch(ctx context.Context, path string) ([]byte, error)

	// StartTransaction opens a candidate datastore (NETCONF) or
	// transaction-scope bundle (gNMI). On transports without
	// transaction support it returns ErrUnsupported so callers fall
	// back to best-effort apply. A zero-value TxHandle indicates a
	// no-transaction apply to subsequent Mutate calls.
	StartTransaction(ctx context.Context) (TxHandle, error)

	// Mutate executes ops in order. A non-nil error short-circuits the
	// sequence at the first failure; the transport is responsible for
	// rolling the candidate (if any) back to the pre-call state.
	Mutate(ctx context.Context, tx TxHandle, ops []Op) error

	// Commit finalises a transaction. For non-transactional transports
	// it is a no-op when tx is the zero value; otherwise ErrUnsupported.
	Commit(ctx context.Context, tx TxHandle) error

	// Discard rolls back an in-flight transaction.
	Discard(ctx context.Context, tx TxHandle) error

	// SaveStartup copies running-config to startup-config on transports
	// that support it; ErrUnsupported otherwise.
	SaveStartup(ctx context.Context) error

	// Close releases any transport-owned resources. Calling Close on an
	// implementation that does not own its underlying session (e.g. the
	// RESTCONF adapter sharing the apphosting HTTP client) must be safe
	// and must NOT tear down that shared session.
	Close() error
}

// ErrUnsupported is returned by transport methods whose capability is
// not expressed by the concrete implementation. Callers inspect the
// transport's Capabilities first; ErrUnsupported is the backstop for
// callers that didn't.
var ErrUnsupported = errors.New("transport: capability unsupported by this implementation")
