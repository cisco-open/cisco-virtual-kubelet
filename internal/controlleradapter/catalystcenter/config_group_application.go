// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

// Preserve the exact source and controller snapshot around actor authorization.
// The shared contract owns the all-member checks and atomic status transition.
func (a *adapter) persistVerifiedConfigurationGroupReceipt(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, nc *cvk.NetworkController, members []*ops.ControllerConfigurationReservation, receipt *cvk.NetworkControllerConfigurationActionApplication, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time, previous ...*cvk.NetworkControllerConfigurationActionApplication) error {
	if len(previous) > 1 {
		return errConfigurationTransition
	}
	if config == nil || source == nil || nc == nil || receipt == nil || authorize == nil {
		return errConfigurationTransition
	}
	// metav1.Time serializes to whole seconds. Retain that same precision before
	// the contract compares the status readback; otherwise a successful write can
	// appear different solely because serialization discarded nanoseconds.
	receipt = receipt.DeepCopy()
	if receipt.VerifiedAt != nil {
		receipt.VerifiedAt.Time = receipt.VerifiedAt.Time.UTC().Truncate(time.Second)
	}
	check := func() error {
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if current.UID != nc.UID || current.ResourceVersion != nc.ResourceVersion {
			return errConfigurationStale
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	guarded := func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
		if err := check(); err != nil {
			return err
		}
		if err := authorize(ctx, current); err != nil {
			return err
		}
		return check()
	}
	if len(previous) == 1 {
		return contract.PersistReplacingVerifiedGroupReceipt(ctx, a.statusReader, a.statusWriter, nc, members, receipt, guarded, now, previous[0])
	}
	return contract.PersistVerifiedGroupReceipt(ctx, a.statusReader, a.statusWriter, nc, members, receipt, guarded, now)
}
