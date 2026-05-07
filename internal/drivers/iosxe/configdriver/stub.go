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

// stubDriver is the Phase-0 placeholder implementation. Open and Close
// succeed so the provider can wire the driver into its lifecycle without
// branching on the scaffold; every method that would touch a device
// returns ErrNotImplemented.
type stubDriver struct{}

// NewStubDriver returns a Driver whose write-path methods all return
// ErrNotImplemented. The intent is to let the provider, reconciler and
// status writers be built and tested end-to-end before the real device
// interaction lands.
func NewStubDriver() Driver { return &stubDriver{} }

// Compile-time check — this will fail to build if the Driver interface
// drifts without a matching update to the stub.
var _ Driver = (*stubDriver)(nil)

func (*stubDriver) Open(context.Context, TransportClient, DeviceInfo) error { return nil }

func (*stubDriver) Close() error { return nil }

func (*stubDriver) Validate(context.Context, ResolvedIntent) error { return ErrNotImplemented }

func (*stubDriver) Fetch(context.Context, []string) (Observed, error) {
	return Observed{}, ErrNotImplemented
}

func (*stubDriver) Plan(context.Context, ResolvedIntent, Observed) (Plan, error) {
	return Plan{}, ErrNotImplemented
}

func (*stubDriver) Apply(context.Context, Plan) (ApplyResult, error) {
	return ApplyResult{}, ErrNotImplemented
}

func (*stubDriver) Rollback(context.Context, RollbackToken) error { return ErrNotImplemented }

func (*stubDriver) SaveStartup(context.Context) error { return ErrNotImplemented }
