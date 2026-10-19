// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"reflect"
	"time"
)

// Independent device evidence must follow the exact native completion boundary
// and cover the admitted incoming/peer/parent footprint. It grants no dispatch,
// lease release or journal mutation authority.
type configurationGroupConvergenceProof func(context.Context, *cvk.NetworkControllerConfigurationOperation, []contract.GroupTargetIdentity, time.Time) error

// Bracket independent proof with the same complete native/managed observation.
// Proof receives private copies so it cannot rewrite the admitted operation.
func observeConfigurationGroupConvergence[T any](ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time, collect func(context.Context) (T, []contract.GroupTargetIdentity, time.Time, error), proof configurationGroupConvergenceProof) (T, error) {
	var zero T
	if op == nil || op.PhysicalGroup == nil || proof == nil || collect == nil || now.IsZero() {
		return zero, errConfigurationDeviceAdmission
	}
	before, targets, completed, err := collect(ctx)
	if err != nil {
		return zero, err
	}
	if completed.IsZero() || completed.After(now.UTC().Truncate(time.Second)) {
		return zero, errConfigurationBusy
	}
	if !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
		return zero, errConfigurationStale
	}
	if err := proof(ctx, op.DeepCopy(), append([]contract.GroupTargetIdentity(nil), targets...), completed); err != nil {
		return zero, err
	}
	after, current, currentCompleted, err := collect(ctx)
	if err != nil {
		return zero, err
	}
	if !completed.Equal(currentCompleted) || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(targets, current) {
		return zero, errConfigurationStale
	}
	return before, nil
}
