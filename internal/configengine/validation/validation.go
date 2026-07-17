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

// Package validation defines the platform-neutral validation boundary for
// device-facing operation graphs. Platform validators may decorate the
// structural validator with release-specific schema checks.
package validation

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

// EnvMode retains the established deployment knob while the validation layer
// is generalized beyond YANG transports.
const EnvMode = "CONFIG_YANG_VALIDATION"

type Mode string

const (
	ModeDisabled Mode = "disabled"
	ModeWarn     Mode = "warn"
	ModeStrict   Mode = "strict"
)

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

func ModeFromEnv() (Mode, error) { return ParseMode(os.Getenv(EnvMode)) }

// Context identifies the canonical model and platform scope associated with
// one operation. AllowedPaths is the legacy IOS XE spelling;
// AllowedWritePrefixes takes precedence when supplied.
type Context struct {
	Platform             string
	Family               string
	DeviceVersion        string
	ReleaseTag           string
	ModelVersion         string
	AllowedPaths         []string
	AllowedWritePrefixes []string
}

type Validator interface {
	ValidateOperation(Context, transport.Op) error
}

// StructuralValidator enforces invariants shared by YANG and DME transports.
// It deliberately does not claim semantic schema conformance; platform
// validators and generated family codecs provide that layer.
type StructuralValidator struct{}

func NewStructuralValidator() *StructuralValidator { return &StructuralValidator{} }

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
	allowed := ctx.AllowedWritePrefixes
	if len(allowed) == 0 {
		allowed = ctx.AllowedPaths
	}
	allowParentCreation := ctx.Platform == "" || strings.EqualFold(ctx.Platform, "iosxe")
	if op.Path != "" && len(allowed) > 0 && !pathAllowed(op.Path, allowed, allowParentCreation) {
		return fmt.Errorf("%s: op path %q is outside writer mutation scope %v", ctx.Family, op.Path, allowed)
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
		return fmt.Errorf("%s: body is not a JSON object: %w", ctx.Family, err)
	}
	if len(body) != 1 {
		return fmt.Errorf("%s: body must have exactly one top-level envelope, got %d", ctx.Family, len(body))
	}
	for envelope := range body {
		if strings.TrimSpace(envelope) == "" {
			return fmt.Errorf("%s: empty operation envelope key", ctx.Family)
		}
	}
	return nil
}

func pathAllowed(path string, allowed []string, allowParentCreation bool) bool {
	base := normalizePath(stripKeySuffix(path))
	for _, candidate := range allowed {
		candidate = normalizePath(stripKeySuffix(candidate))
		if candidate == "" {
			continue
		}
		switch {
		case base == candidate:
			return true
		case strings.HasPrefix(base, candidate+"/"):
			return true
		case allowParentCreation && strings.HasPrefix(candidate, base+"/"):
			// IOS XE writers may emit a parent-container creation op.
			return true
		}
	}
	return false
}

func normalizePath(path string) string {
	return strings.Trim(strings.TrimSpace(path), "/")
}

func stripKeySuffix(path string) string {
	if i := strings.IndexByte(path, '='); i >= 0 {
		return path[:i]
	}
	return path
}
