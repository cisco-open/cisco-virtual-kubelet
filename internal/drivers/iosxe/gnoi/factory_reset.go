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

package gnoi

import (
	"context"
	"fmt"
)

// FactoryResetOpts mirrors the Start RPC inputs.
type FactoryResetOpts struct {
	FactoryOS   bool // roll back to the OS that shipped from factory
	ZeroFill    bool // zero-fill persistent storage
	RetainCerts bool // preserve cert.proto-installed certificates
}

// FactoryReset triggers gNOI FactoryReset.Start. There is no
// pre-flight read-only probe for this service — callers should pin
// the capability via CapabilityCache.Pin if they have prior knowledge,
// otherwise the cache leaves the service marked "unknown" (assume
// supported until proven otherwise) and learns from the RPC response.
//
// IMPORTANT: this is the most destructive RPC in the gNOI catalogue.
// The Phase-B build of the gNOI client intentionally leaves it
// unimplemented; Phase D adds it behind the IOSXEOperationalAction
// CRD's write-class RBAC gate.
func (c *Client) FactoryReset(ctx context.Context, opts FactoryResetOpts) error {
	return fmt.Errorf("gnoi FactoryReset.Start: not implemented in Phase B; see IOSXEOperationalAction in Phase D")
}
