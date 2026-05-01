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

// Transactional view of a transport.Interface that pre-binds an active
// TxHandle. Writers all call `transport.Mutate(ctx, "", ops)` with an
// empty handle — the no-transaction default — because they don't have
// any way to know whether the engine has opened a transaction. The
// view's `Mutate` method substitutes the captured handle on each call,
// which is how the engine threads transactional semantics through
// existing writer implementations without changing every writer.
//
// This pattern is preferred over adding a `tx TxHandle` parameter to
// `writers.SectionWriter.Apply`: that would touch every writer file,
// every test, and every future platform writer. The wrapper keeps the
// blast radius inside the engine package.
//
// External-review Finding #1: before this wiring, `spec.transactional`
// existed on the API but was inert. The engine called Apply directly
// against the unwrapped transport, so NETCONF (the only transport that
// actually supports candidate+commit) wrote running directly with no
// rollback on failure. With the view, NETCONF writers see a transport
// whose `Mutate` calls land in the candidate datastore and the engine
// commits/discards once at the end of the tick.

import (
	"context"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// transactionalView wraps a transport.Interface and pre-binds a
// TxHandle so writers that call Mutate with the empty handle land
// their ops inside the active transaction. Every other method passes
// through unchanged so Capabilities-driven branches in writers (e.g.
// the no-op SubscribeCapable check) still see the underlying
// transport's capabilities.
type transactionalView struct {
	inner transport.Interface
	tx    transport.TxHandle
}

func newTransactionalView(inner transport.Interface, tx transport.TxHandle) *transactionalView {
	return &transactionalView{inner: inner, tx: tx}
}

func (v *transactionalView) Capabilities() transport.Capabilities {
	return v.inner.Capabilities()
}

// Fetch reads through the transaction's working datastore when the
// inner transport advertises the TxFetcher capability (NETCONF
// candidate today; gNMI staged-buffer when that lands). Transports
// without a working-datastore concept (RESTCONF — running is the
// only surface) fall through to plain Fetch and the engine's
// verify-Diff reads running.
//
// Wave 1A-fu (external-review-followup Finding #1). Before this
// the verify-Fetch always read running, missed the writes still
// pending in candidate, reported residual drift, and Discard'd the
// candidate — transactional applies could not reliably reach
// Commit even when the writes themselves would have committed
// cleanly.
func (v *transactionalView) Fetch(ctx context.Context, path string) ([]byte, error) {
	if tf, ok := v.inner.(transport.TxFetcher); ok {
		return tf.FetchTx(ctx, v.tx, path)
	}
	return v.inner.Fetch(ctx, path)
}

// StartTransaction is intentionally not delegated. Nesting transactions
// is not supported by the underlying transports, and surfacing the
// error here catches any future writer that tries to open its own
// transaction inside the engine-managed scope.
func (v *transactionalView) StartTransaction(_ context.Context) (transport.TxHandle, error) {
	return "", transport.ErrUnsupported
}

// Mutate is the load-bearing method. The captured handle replaces
// whatever the caller passed (writers pass "" today; that's exactly
// the case we want to rewrite).
func (v *transactionalView) Mutate(ctx context.Context, _ transport.TxHandle, ops []transport.Op) error {
	return v.inner.Mutate(ctx, v.tx, ops)
}

// Commit/Discard are also not delegated — the engine is the sole
// owner of the transaction lifecycle. A writer that calls these
// against the view would short-circuit the engine's commit/discard
// orchestration, so we report unsupported instead.
func (v *transactionalView) Commit(_ context.Context, _ transport.TxHandle) error {
	return transport.ErrUnsupported
}

func (v *transactionalView) Discard(_ context.Context, _ transport.TxHandle) error {
	return transport.ErrUnsupported
}

func (v *transactionalView) SaveStartup(ctx context.Context) error {
	return v.inner.SaveStartup(ctx)
}

// Close on the view is a no-op — the underlying transport's lifecycle
// is owned by the engine's caller (the per-device reconciler loop or
// the aggregator worker), not by a single tick.
func (v *transactionalView) Close() error {
	return nil
}
