// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package aggregator

import (
	"context"
	"errors"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
)

// The existing engine invokes this before every write transaction, including
// relinquishment cleanup. It is an absence check, not a held lease or proof an
// earlier transaction drained; finishing never releases bootstrap evidence.
func (r *AggregatedReconciler) bootstrapMutationGuard(device *cvk.CiscoDevice) devicecoordination.MutationGuard {
	if r.BootstrapFenceNamespace == "" {
		return nil
	}
	expected := device.DeepCopy()
	return func(ctx context.Context) (context.Context, func(error), error) {
		if r.APIReader == nil || expected == nil {
			return ctx, nil, errors.New("aggregator bootstrap guard requires uncached device identity")
		}
		if err := contract.CheckBootstrapDeviceEnrollment(ctx, r.APIReader, r.BootstrapFenceNamespace, expected); err != nil {
			return ctx, nil, err
		}
		return ctx, func(error) {}, nil
	}
}
