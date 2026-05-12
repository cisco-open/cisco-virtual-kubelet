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

	resetpb "github.com/openconfig/gnoi/factory_reset"
)

// FactoryResetOpts mirrors the Start RPC inputs.
type FactoryResetOpts struct {
	FactoryOS   bool // roll back to the OS that shipped from factory
	ZeroFill    bool // zero-fill persistent storage
	RetainCerts bool // preserve cert.proto-installed certificates
}

// FactoryResetError wraps a typed device-side ResetError so reconcilers
// can distinguish "not supported" from "operator-input invalid".
type FactoryResetError struct {
	Detail string
	// Codes are surfaced verbatim from the device for diagnostic
	// fidelity — typical values include "factory_os" / "zero_fill" /
	// "other" indicating which optional flag the device rejected.
	Codes []string
}

func (e *FactoryResetError) Error() string {
	if len(e.Codes) > 0 {
		return fmt.Sprintf("gnoi FactoryReset.Start error (%v): %s", e.Codes, e.Detail)
	}
	return fmt.Sprintf("gnoi FactoryReset.Start error: %s", e.Detail)
}

// FactoryReset triggers gNOI FactoryReset.Start. This is the single
// most destructive RPC in the catalogue: it wipes the device and
// reboots to factory defaults. Callers (the IOSXEOperationalAction
// reconciler with write-class RBAC) must validate operator intent
// before invocation.
func (c *Client) FactoryReset(ctx context.Context, opts FactoryResetOpts) error {
	if err := c.cap.ensureSupported(ServiceFactoryReset); err != nil {
		return err
	}
	req := &resetpb.StartRequest{
		FactoryOs:   opts.FactoryOS,
		ZeroFill:    opts.ZeroFill,
		RetainCerts: opts.RetainCerts,
	}
	resp, err := c.reset.Start(c.authCtx(ctx), req)
	c.cap.Observe(ServiceFactoryReset, err)
	if err != nil {
		return fmt.Errorf("gnoi FactoryReset.Start: %w", err)
	}
	if resp == nil {
		return nil
	}
	if errBlock := resp.GetResetError(); errBlock != nil {
		out := &FactoryResetError{Detail: errBlock.Detail}
		if errBlock.FactoryOsUnsupported {
			out.Codes = append(out.Codes, "factory_os_unsupported")
		}
		if errBlock.ZeroFillUnsupported {
			out.Codes = append(out.Codes, "zero_fill_unsupported")
		}
		if errBlock.Other {
			out.Codes = append(out.Codes, "other")
		}
		return out
	}
	return nil
}
