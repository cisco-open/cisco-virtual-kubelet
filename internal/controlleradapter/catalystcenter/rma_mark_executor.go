// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// The enclosing inventory plan uses recordCompletion instead of generic
// Verified advancement to retain the native record UUID.
// Dispatch still requires the shared physical reservation and journal protocol.
type deviceReplacementMarkExecutor struct {
	adapter   *adapter
	config    *configapi.NetworkControllerConfig
	source    *configSourceSnapshot
	reference configurationDeviceReference
	desired   deviceReplacementIntent
}

func (*deviceReplacementMarkExecutor) count() int { return 1 }
func (e *deviceReplacementMarkExecutor) next(ctx context.Context) (*configurationWrite, error) {
	if e == nil {
		return nil, errConfigurationTransition
	}
	return nextPreparedInventoryWrite(ctx, e.adapter, e.config, e.source, func(ctx context.Context) (*configurationWrite, error) {
		return e.adapter.planOwnedDeviceReplacementMark(ctx, e.config, e.source, e.reference, e.desired)
	}, func(ctx context.Context) (*configurationWrite, error) {
		return e.adapter.client.planDeviceReplacementMark(ctx, e.reference, e.desired)
	})
}

func (e *deviceReplacementMarkExecutor) observeCompletion(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	if e == nil || e.adapter == nil || op == nil || op.Phase != "Accepted" {
		return false, errConfigurationTransition
	}
	evidence, err := e.adapter.observeOwnedDeviceReplacementMark(ctx, e.config, e.source, e.reference, e.desired, op, now)
	return evidence != nil, err
}
func (e *deviceReplacementMarkExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if e == nil || e.adapter == nil || op == nil || op.Phase != "Verifying" {
		return false, errConfigurationTransition
	}
	evidence, err := e.adapter.observeOwnedDeviceReplacementMark(ctx, e.config, e.source, e.reference, e.desired, op, time.Now())
	return evidence != nil, err
}
func (e *deviceReplacementMarkExecutor) recordCompletion(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) error {
	if e == nil || e.adapter == nil || op == nil || op.Phase != "Verifying" {
		return errConfigurationTransition
	}
	// The adapter re-reads the authoritative operation by ID and brackets native
	// observation with source/owner checks before atomic receipt persistence.
	return e.adapter.recordOwnedDeviceReplacementMark(ctx, e.config, e.source, e.reference, e.desired, op.ID, time.Now())
}

var _ configurationExecutor = (*deviceReplacementMarkExecutor)(nil)

func (e *deviceReplacementMarkExecutor) intent() (configurationDeviceReference, deviceReplacementIntent, error) {
	return e.reference, e.desired, nil
}
func (e *deviceReplacementMarkExecutor) validateSettled(state *cvk.NetworkControllerConfigurationStatus, snapshot *deviceReplacementSnapshot) error {
	_, err := deviceReplacementMarkApplied(state, e.config, snapshot, time.Now())
	return err
}
