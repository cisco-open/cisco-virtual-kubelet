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

// Package validation decorates the common operation validator with IOS XE
// release-specific YANG profiles.
package validation

import (
	"encoding/json"

	commonvalidation "github.com/cisco/virtual-kubelet-cisco/internal/configengine/validation"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

const EnvMode = commonvalidation.EnvMode

type (
	Mode      = commonvalidation.Mode
	Context   = commonvalidation.Context
	Validator = commonvalidation.Validator
)

const (
	ModeDisabled Mode = commonvalidation.ModeDisabled
	ModeWarn     Mode = commonvalidation.ModeWarn
	ModeStrict   Mode = commonvalidation.ModeStrict
)

func ParseMode(raw string) (Mode, error) { return commonvalidation.ParseMode(raw) }
func ModeFromEnv() (Mode, error)         { return commonvalidation.ModeFromEnv() }

type StructuralValidator struct {
	common *commonvalidation.StructuralValidator
}

func NewStructuralValidator() *StructuralValidator {
	return &StructuralValidator{common: commonvalidation.NewStructuralValidator()}
}

func (v *StructuralValidator) ValidateOperation(ctx Context, op transport.Op) error {
	if v == nil || v.common == nil {
		v = NewStructuralValidator()
	}
	if err := v.common.ValidateOperation(ctx, op); err != nil {
		return err
	}
	if op.Verb == transport.VerbCLI || op.Verb == transport.VerbDelete {
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal(op.Body, &body); err != nil {
		return err // common validation normally returns this first
	}
	for envelope, payload := range body {
		return validateReleaseProfile(ctx, envelope, payload)
	}
	return nil
}
