// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Cleanup never derives ownership from native absence. The retained original
// mark must match the immutable cleanup binding and precede its submission.
func ownedRMAUnmarkOrigin(state *cvk.NetworkControllerConfigurationStatus, config *configapi.NetworkControllerConfig, op *cvk.NetworkControllerConfigurationOperation, now time.Time) error {
	if op == nil || op.Action != "Delete" || op.Section != "inventory" || (op.Phase != "Accepted" && op.Phase != "Verifying" && op.Phase != "Verified") || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(now) {
		return errConfigurationTransition
	}
	binding, err := parseRMAUnmarkResource(op.Resource)
	if err != nil {
		return err
	}
	receipt, err := ownedRMAMarkReceipt(state, config, binding.scope, op.TargetID, op.TargetSerial, op.ParentID, now)
	if err != nil {
		return err
	}
	if receipt == nil {
		return errConfigurationOwned
	}
	if receipt.Resource != "rma-mark-record:"+binding.recordID || receipt.Operation.ID == op.ID || receipt.VerifiedAt.After(op.SubmittedAt.Time) || binding.createdAt < receipt.Operation.SubmittedAt.Add(-configurationTaskClockSkew).UnixMilli() || binding.createdAt > receipt.VerifiedAt.Add(configurationTaskClockSkew).UnixMilli() {
		return errConfigurationStale
	}
	return nil
}

func (a *adapter) observeOwnedDeviceReplacementUnmark(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	ref, want, err := rmaUnmarkPruneIntent(config, source, name)
	if err != nil {
		return false, err
	}
	return a.observeOwnedInventoryDeployment(ctx, config, source, op, func(ctx context.Context) (bool, error) {
		nc, err := a.currentController(ctx)
		if err != nil {
			return false, err
		}
		if err := ownedRMAUnmarkOrigin(nc.Status.Configuration, config, op, now); err != nil {
			return false, err
		}
		return a.client.observeDeviceReplacementUnmark(ctx, ref, want, op, now)
	})
}

// Retain both mark and cleanup receipts through the shared atomic writer.
// Settlement and ownership retirement remain separate from native completion.
func (a *adapter) recordOwnedDeviceReplacementUnmark(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name, operationID string, now time.Time) error {
	return a.recordOwnedInventoryDeployment(ctx, config, source, operationID, func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
		return a.observeOwnedDeviceReplacementUnmark(ctx, config, source, name, op, now)
	})
}
