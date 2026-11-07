// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package common

import (
	"errors"
	"fmt"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// ErrVerifiedArtifactsUnsupported prevents Verified configuration from falling
// through to the Standard install/recovery path. Metadata verification alone
// cannot prove the bytes installed on a device. Remove this gate only when a
// platform implements verified delivery and durable installation recovery.
var ErrVerifiedArtifactsUnsupported = errors.New("VerifiedArtifactsUnsupported: verified delivery runtime is unavailable for this driver or configuration")

// RequireStandardAppArtifacts gates forward app mutations, including recovery.
// Read-only inventory and deletion intentionally do not use this gate.
func RequireStandardAppArtifacts(spec *v1alpha1.DeviceSpec) error {
	if spec == nil {
		return nil
	}
	if spec.AppArtifacts.EffectiveMode() == v1alpha1.AppArtifactModeVerified {
		return ErrVerifiedArtifactsUnsupported
	}
	if err := spec.AppArtifacts.Validate(); err != nil {
		return fmt.Errorf("appArtifacts: %w", err)
	}
	return nil
}

// CheckAppArtifactSupport permits only drivers with an explicitly wired runtime.
func CheckAppArtifactSupport(spec *v1alpha1.DeviceSpec, driver any) error {
	if spec != nil && spec.AppArtifacts.EffectiveMode() == v1alpha1.AppArtifactModeVerified {
		if capable, ok := driver.(interface{ VerifiedAppArtifactsEnabled() bool }); ok && capable.VerifiedAppArtifactsEnabled() {
			return spec.AppArtifacts.Validate()
		}
	}
	return RequireStandardAppArtifacts(spec)
}
