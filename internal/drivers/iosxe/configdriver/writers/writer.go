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

package writers

import (
	"context"
	"errors"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// ErrNotImplemented is returned by Phase-0 writer skeletons. It wraps the
// driver-level sentinel so callers can use errors.Is(err,
// configdriver.ErrNotImplemented) uniformly regardless of which package
// surfaced the failure.
var ErrNotImplemented = errors.Join(errUnimplementedWriter, configdriver.ErrNotImplemented)

// errUnimplementedWriter is the distinguishing leaf error so callers that
// care which layer was unimplemented can tell without string matching.
var errUnimplementedWriter = errors.New("iosxe writer: not implemented in Phase 0 scaffold")

// SectionWriter is the per-family interface. Implementations are expected
// to be stateless — any device session state lives in the TransportClient
// the driver passes in. Family() and YANGPaths() must be pure functions so
// the registry can be queried without side effects.
type SectionWriter interface {
	// Family returns the netascode family name. The value must match the
	// corresponding key in schema/families.yaml.
	Family() string

	// YANGPaths returns the set of Cisco-IOS-XE YANG xpaths the writer
	// reads from and writes to. Used by the driver core to fan out GETs
	// in parallel and to scope device locks on the NETCONF path.
	YANGPaths() []string

	// Fetch reads the family's current state from the device. The
	// returned value is opaque — it is passed verbatim to Diff() and
	// never inspected by the driver core.
	Fetch(ctx context.Context, c configdriver.TransportClient) (any, error)

	// Diff computes the operations required to move observed → desired.
	// Returning an empty slice signals no change. Neither argument is
	// retained by the writer after the call.
	Diff(desired, observed any) ([]transport.Op, error)

	// Apply executes the operations produced by Diff in order. A non-nil
	// error short-circuits the family's portion of the plan; the driver
	// core then invokes Rollback with the PreImage captured earlier.
	Apply(ctx context.Context, c configdriver.TransportClient, ops []transport.Op) error
}

// PruneCapable is an optional interface a writer implements when it
// can safely emit DELETE ops for entries present on the device but
// absent from the resolved intent. The engine consults this only
// when the IOSXEConfig CR sets spec.pruneOnRelinquish: true, so
// writers can roll out support family-by-family without the engine
// or the CR vocabulary growing per-family flags.
//
// The contract for PruneDiff matches Diff except that the returned
// ops describe deletions only — additive ops are still produced by
// Diff, and the engine concatenates the two slices in order
// (additive first, prune second). Returning an empty slice means
// "nothing to prune".
type PruneCapable interface {
	PruneDiff(desired, observed any) ([]transport.Op, error)
}
