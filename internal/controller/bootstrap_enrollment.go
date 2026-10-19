// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

// This guard is deliberately before finalizers, teardown, projection, generated
// access and worker/config writes. It does not stop an already-running worker,
// resolve unknown hardware aliases or replace the bootstrap dispatcher's global
// identity check. Only the manager supplies the shared namespace.
func (r *CiscoDeviceReconciler) ensureBootstrapEnrollmentClear(ctx context.Context, device *cvk.CiscoDevice) error {
	if r.BootstrapFenceNamespace == "" || device == nil {
		return nil
	}
	return contract.CheckBootstrapDeviceEnrollment(ctx, r.APIReader, r.BootstrapFenceNamespace, device)
}
