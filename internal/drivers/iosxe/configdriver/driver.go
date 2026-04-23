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

package configdriver

import "context"

// Driver reconciles IOS-XE device configuration against an IOSXEConfig CR.
//
// The interface is split into lifecycle (Open/Close), read (Fetch), compute
// (Plan), write (Apply/Rollback/SaveStartup), and pure validation
// (Validate) so that unit tests can exercise each stage in isolation
// against synthetic inputs without a live device.
//
// Phase-0 scaffold: the default implementation returns ErrNotImplemented
// from every method that touches a device. A compile-time assertion in
// stub.go pins the stub to this contract.
type Driver interface {
	// Open binds the driver to a transport and device. It is called once
	// per device, after the apphosting driver has already handshaken, so
	// implementations can assume the credentials are valid.
	Open(ctx context.Context, c TransportClient, dev DeviceInfo) error

	// Close releases driver-local resources. The transport is owned by
	// the caller and must not be closed here.
	Close() error

	// Validate checks a resolved intent for problems that can be detected
	// without touching the device (missing required leaves, type
	// mismatches, unsupported families for the target YANG release).
	Validate(ctx context.Context, intent ResolvedIntent) error

	// Fetch reads the current device state for the listed families and
	// returns it as an Observed snapshot. Families outside the list are
	// not queried.
	Fetch(ctx context.Context, families []string) (Observed, error)

	// Plan produces the ordered operation list required to move the device
	// from observed → intent. An empty Plan.Operations slice means no
	// change is needed. Plan MUST be side-effect-free.
	Plan(ctx context.Context, intent ResolvedIntent, observed Observed) (Plan, error)

	// Apply executes the plan against the device. It returns a non-nil
	// ApplyResult even on failure so partial progress can be recorded.
	Apply(ctx context.Context, plan Plan) (ApplyResult, error)

	// Rollback restores the pre-apply state identified by token. On
	// transports without a candidate datastore (RESTCONF) this is a
	// best-effort replay of the plan's PreImage.
	Rollback(ctx context.Context, token RollbackToken) error

	// SaveStartup copies running-config to startup-config. Only called
	// when spec.writeStartup is true and the apply succeeded.
	SaveStartup(ctx context.Context) error
}
