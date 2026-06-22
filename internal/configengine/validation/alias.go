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

// Package validation exposes the platform-neutral validation boundary for
// writer-produced device operations. It aliases the current IOS-XE
// implementation during the config engine extraction.
package validation

import iosxevalidation "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/validation"

const EnvMode = iosxevalidation.EnvMode

type (
	Mode                = iosxevalidation.Mode
	Validator           = iosxevalidation.Validator
	Context             = iosxevalidation.Context
	StructuralValidator = iosxevalidation.StructuralValidator
)

const (
	ModeDisabled Mode = iosxevalidation.ModeDisabled
	ModeWarn     Mode = iosxevalidation.ModeWarn
	ModeStrict   Mode = iosxevalidation.ModeStrict
)

func ParseMode(raw string) (Mode, error) {
	return iosxevalidation.ParseMode(raw)
}

func ModeFromEnv() (Mode, error) {
	return iosxevalidation.ModeFromEnv()
}

func NewStructuralValidator() *StructuralValidator {
	return iosxevalidation.NewStructuralValidator()
}
