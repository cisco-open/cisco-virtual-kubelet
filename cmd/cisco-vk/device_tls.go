// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/sha256"
	"fmt"
	"os"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/tlsutil"
)

// Bind the bytes actually mounted by kubelet, not merely Secret metadata.
// This check runs before clients/background work. Rotation is also fenced by
// current Secret resourceVersion at the managed software-mutation boundary.
func validateDeviceTLSCAProjection(spec *ciskov1.DeviceSpec, contract, revision, digest string) error {
	if contract == "" && revision == "" && digest == "" {
		return nil
	}
	if contract != managedprotocol.DeviceTLSCAProjectionVersion || revision == "" || revision == "missing" || len(digest) != 64 {
		return fmt.Errorf("device TLS CA projection requires the supported contract, current Secret revision and SHA-256")
	}
	if spec == nil || spec.TLS == nil || !spec.TLS.Enabled || spec.TLS.InsecureSkipVerify || spec.TLS.CASecretRef != nil || spec.TLS.CAFile == "" {
		return fmt.Errorf("device TLS CA projection requires verified TLS and a resolved CA file")
	}
	material, err := os.ReadFile(spec.TLS.CAFile)
	if err != nil {
		return fmt.Errorf("read projected device TLS CA: %w", err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(material)) != digest {
		return fmt.Errorf("projected device TLS CA bytes do not match the manager-bound SHA-256")
	}
	return tlsutil.ValidatePublicCABundle(material)
}
