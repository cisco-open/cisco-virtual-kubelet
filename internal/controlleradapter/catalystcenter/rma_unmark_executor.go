// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"time"
)

// Cleanup uses the same exact-operation executor contract as mark and template
// deployment. Returning a write does not grant dispatch authority: the shared
// reconciler must prepare, reserve and claim it before submission.
type deviceReplacementUnmarkExecutor struct {
	adapter *adapter
	config  *configapi.NetworkControllerConfig
	source  *configSourceSnapshot
	name    string
}

func (*deviceReplacementUnmarkExecutor) count() int { return 1 }
func (e *deviceReplacementUnmarkExecutor) next(ctx context.Context) (*configurationWrite, error) {
	if e == nil {
		return nil, errConfigurationTransition
	}
	return nextPreparedInventoryWrite(ctx, e.adapter, e.config, e.source, func(ctx context.Context) (*configurationWrite, error) {
		return e.adapter.planOwnedDeviceReplacementUnmark(ctx, e.config, e.source, e.name)
	}, func(ctx context.Context) (*configurationWrite, error) {
		return e.adapter.planOwnedNativeDeviceReplacementUnmark(ctx, e.config, e.source, e.name)
	})
}
func (e *deviceReplacementUnmarkExecutor) observeCompletion(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	if e == nil || e.adapter == nil || op == nil || op.Phase != "Accepted" {
		return false, errConfigurationTransition
	}
	return e.adapter.observeOwnedDeviceReplacementUnmark(ctx, e.config, e.source, e.name, op, now)
}
func (e *deviceReplacementUnmarkExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if e == nil || e.adapter == nil || op == nil || op.Phase != "Verifying" {
		return false, errConfigurationTransition
	}
	return e.adapter.observeOwnedDeviceReplacementUnmark(ctx, e.config, e.source, e.name, op, time.Now())
}
func (e *deviceReplacementUnmarkExecutor) recordCompletion(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) error {
	if e == nil || e.adapter == nil || op == nil || op.Phase != "Verifying" {
		return errConfigurationTransition
	}
	return e.adapter.recordOwnedDeviceReplacementUnmark(ctx, e.config, e.source, e.name, op.ID, time.Now())
}

var _ configurationExecutor = (*deviceReplacementUnmarkExecutor)(nil)

func (e *deviceReplacementUnmarkExecutor) intent() (configurationDeviceReference, deviceReplacementIntent, error) {
	return rmaUnmarkPruneIntent(e.config, e.source, e.name)
}
func (e *deviceReplacementUnmarkExecutor) validateSettled(state *cvk.NetworkControllerConfigurationStatus, snapshot *deviceReplacementSnapshot) error {
	cleanup, err := deviceReplacementUnmarkApplied(state, e.config, snapshot, time.Now())
	if err != nil {
		return err
	}
	if cleanup != nil {
		return nil
	}
	marked, err := deviceReplacementMarkApplied(state, e.config, snapshot, time.Now())
	if err != nil {
		return err
	}
	if !marked {
		return errConfigurationOwned
	}
	return nil
}
