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

	ospb "github.com/openconfig/gnoi/os"
)

// OSVerifyResult mirrors gNOI OS.Verify.
type OSVerifyResult struct {
	// Version is the currently-running OS version reported by the
	// device. For IOS-XE this is the SPA bundle version (e.g.
	// "17.15.01a").
	Version string

	// ActivationFailMessage carries the device's explanation when the
	// last activate did not yield the requested version. IOS-XE has
	// historically returned this as an empty string even on failure,
	// so callers cross-check via gNMI Get on
	// Cisco-IOS-XE-native:native/version.
	ActivationFailMessage string

	// IndividualSupervisorInstall, when true, signals that each
	// supervisor on a dual-RP device requires its own Install.
	IndividualSupervisorInstall bool
}

// Verify returns the running OS version. Read-only — used as the OS
// service capability probe.
func (c *Client) Verify(ctx context.Context) (*OSVerifyResult, error) {
	if err := c.cap.ensureSupported(ServiceOS); err != nil {
		return nil, err
	}
	resp, err := c.os.Verify(c.authCtx(ctx), &ospb.VerifyRequest{})
	c.cap.Observe(ServiceOS, err)
	if err != nil {
		return nil, fmt.Errorf("gnoi OS.Verify: %w", err)
	}
	return &OSVerifyResult{
		Version:                     resp.Version,
		ActivationFailMessage:       resp.ActivationFailMessage,
		IndividualSupervisorInstall: resp.IndividualSupervisorInstall,
	}, nil
}

// Install, Activate, and the streaming TransferProgress wiring belong
// to Phase C of the gNOI pillar rollout — they need a multi-stage
// reconciler driving them, not a one-shot RPC wrapper. The stubs
// below exist to keep the Client surface complete; their
// implementations are intentionally deferred.

// Install initiates the gNOI OS.Install bidi stream. Phase C will
// implement the full TransferRequest / TransferContent / TransferEnd
// orchestration; for now this returns a not-implemented error to
// make accidental Phase-B callers fail loudly.
func (c *Client) Install(ctx context.Context) error {
	return fmt.Errorf("gnoi OS.Install: not implemented in Phase B; see IOSXESoftwareUpgrade reconciler in Phase C")
}

// Activate is the Phase-C counterpart to Install.
func (c *Client) Activate(ctx context.Context, version string, noReboot bool) error {
	return fmt.Errorf("gnoi OS.Activate: not implemented in Phase B; see IOSXESoftwareUpgrade reconciler in Phase C")
}
