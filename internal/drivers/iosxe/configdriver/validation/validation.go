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

// Package validation owns the device-facing YANG validation boundary for
// NetAsCode configuration writes.
//
// The public IOSXEConfig model remains NetAsCode-shaped and release-stable.
// Validators run after a writer has translated that intent into IOS-XE YANG
// JSON and before the transport mutates the device. Today the default
// validator is structural and profile-driven; release-specific ygot/ytypes
// validators can plug into the same interface as generated model coverage
// expands.
package validation

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// EnvMode is the deployment-level switch for the configdriver YANG
// validation boundary. It is intentionally process-wide rather than part of
// the NetAsCode CRD: validation is a controller safety policy, not a
// difference in desired device state.
const EnvMode = "CONFIG_YANG_VALIDATION"

// Mode controls how validation results affect a reconcile.
type Mode string

const (
	// ModeDisabled bypasses the validation boundary. This is the default so
	// existing clusters do not see a behaviour change when upgrading.
	ModeDisabled Mode = "disabled"
	// ModeWarn logs validation failures and continues. Useful while adding a
	// new release profile against live devices.
	ModeWarn Mode = "warn"
	// ModeStrict turns validation failures into reconcile errors before any
	// device mutation is attempted.
	ModeStrict Mode = "strict"
)

// ParseMode converts a user-supplied mode string into a Mode. Empty means the
// backward-compatible default.
func ParseMode(raw string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(ModeDisabled), "off", "false", "0":
		return ModeDisabled, nil
	case string(ModeWarn), "warning":
		return ModeWarn, nil
	case string(ModeStrict), "true", "1":
		return ModeStrict, nil
	default:
		return ModeDisabled, fmt.Errorf("%s=%q is invalid; expected disabled, warn, or strict", EnvMode, raw)
	}
}

// ModeFromEnv reads EnvMode and returns the parsed mode.
func ModeFromEnv() (Mode, error) {
	return ParseMode(os.Getenv(EnvMode))
}

// Validator validates one transport operation after NetAsCode intent has been
// translated to device-facing YANG JSON.
type Validator interface {
	ValidateOperation(Context, transport.Op) error
}

// Context describes the resolved release and writer scope for a validation
// call. ReleaseTag is the schema/yang-versions.yaml tag (for example "1718"
// or "2601"). AllowedPaths should be the writer's YANGPaths() for the same
// device version.
type Context struct {
	Family        string
	DeviceVersion string
	ReleaseTag    string
	AllowedPaths  []string
}

// StructuralValidator is the default validation boundary. It enforces
// transport-op invariants common to every family, then applies narrowly-scoped
// release profiles for known YANG shape divergences. It deliberately stops
// short of pretending to be a full YANG compiler; generated ygot/ytypes
// validators should be registered behind this same interface as release
// coverage matures.
type StructuralValidator struct{}

// NewStructuralValidator returns the default validator implementation.
func NewStructuralValidator() *StructuralValidator { return &StructuralValidator{} }

// ValidateOperation validates a single operation.
func (v *StructuralValidator) ValidateOperation(ctx Context, op transport.Op) error {
	if op.Verb == transport.VerbCLI {
		return nil
	}
	switch op.Verb {
	case transport.VerbMerge, transport.VerbReplace, transport.VerbDelete:
	default:
		return fmt.Errorf("%s: unsupported verb %q", ctx.Family, op.Verb)
	}
	if strings.TrimSpace(op.Path) == "" && len(op.PathSpec) == 0 {
		return fmt.Errorf("%s: op has neither Path nor PathSpec", ctx.Family)
	}
	if op.Path != "" && len(ctx.AllowedPaths) > 0 && !pathAllowed(op.Path, ctx.AllowedPaths) {
		return fmt.Errorf("%s: op path %q is outside writer YANG paths %v", ctx.Family, op.Path, ctx.AllowedPaths)
	}
	if op.Verb == transport.VerbDelete {
		if len(op.Body) != 0 {
			return fmt.Errorf("%s: DELETE op carries unexpected body", ctx.Family)
		}
		return nil
	}
	if len(op.Body) == 0 {
		return fmt.Errorf("%s: %s op has empty body", ctx.Family, op.Verb)
	}
	var body map[string]any
	if err := json.Unmarshal(op.Body, &body); err != nil {
		return fmt.Errorf("%s: body is not JSON object: %w", ctx.Family, err)
	}
	if len(body) != 1 {
		return fmt.Errorf("%s: body must have exactly one top-level YANG envelope, got %d", ctx.Family, len(body))
	}
	for envelope, payload := range body {
		if strings.TrimSpace(envelope) == "" {
			return fmt.Errorf("%s: empty YANG envelope key", ctx.Family)
		}
		if err := validateReleaseProfile(ctx, envelope, payload); err != nil {
			return err
		}
	}
	return nil
}

func pathAllowed(path string, allowed []string) bool {
	base := stripKeySuffix(path)
	for _, candidate := range allowed {
		candidate = stripKeySuffix(candidate)
		switch {
		case base == candidate:
			return true
		case strings.HasPrefix(base, candidate+"/"):
			return true
		case strings.HasPrefix(candidate, base+"/"):
			return true
		}
	}
	return false
}

func stripKeySuffix(path string) string {
	if i := strings.IndexByte(path, '='); i >= 0 {
		return path[:i]
	}
	return path
}
