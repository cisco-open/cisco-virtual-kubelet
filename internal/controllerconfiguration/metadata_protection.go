// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const metadataProtectionAnnotation = "ops.cisco.vk/metadata-handoff-protocol"
const metadataProtectionSpecDigest = "e0621483a43538ffe5fda7d3238bc4698d6bf796716b46e6f9794138fda70be7"

// ReadMetadataHandoffProtection requires an uncached manager reader and verifies
// the exact compiled policy/binding, observed compiler status and shared namespace
// incarnation. It supplies read-only installation evidence, never native/fence
// dispatch, worker version compatibility or proof of actual fence settlement.
func ReadMetadataHandoffProtection(ctx context.Context, reader client.Reader, policyName, managerUsername, leaseNamespace string) (*AdmissionProtection, error) {
	return readRetainedAdmissionProtection(ctx, reader, policyName, managerUsername, leaseNamespace, metadataProtectionAnnotation, cvk.NetworkControllerMetadataHandoffProtocol, metadataProtectionSpecDigest)
}
