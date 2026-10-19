// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"time"
)

// Private executor, sharing journal/resume checks with regular templates.
// Compiler ordering and physical admission remain separate requirements.
type compositeTemplateExecutor struct {
	adapter *adapter
	config  *configapi.NetworkControllerConfig
	source  *configSourceSnapshot
	device  inventoryDeviceIntent
	target  compositeTemplateTarget
}

func (*compositeTemplateExecutor) count() int { return 1 }
func (e *compositeTemplateExecutor) next(ctx context.Context) (*configurationWrite, error) {
	if e == nil {
		return nil, errConfigurationTransition
	}
	return nextPreparedInventoryWrite(ctx, e.adapter, e.config, e.source, func(ctx context.Context) (*configurationWrite, error) {
		return e.adapter.planOwnedCompositeTemplateDeployment(ctx, e.config, e.source, e.device, e.target)
	}, func(ctx context.Context) (*configurationWrite, error) {
		return e.adapter.client.planCompositeTemplateDeployment(ctx, e.device, e.target)
	})
}
func (e *compositeTemplateExecutor) observeCompletion(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	if e == nil || e.adapter == nil || op == nil || op.Phase != "Accepted" {
		return false, errConfigurationTransition
	}
	return e.adapter.observeOwnedCompositeTemplateDeployment(ctx, e.config, e.source, e.device, e.target, op, now)
}
func (e *compositeTemplateExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if e == nil || e.adapter == nil || op == nil || op.Phase != "Verifying" {
		return false, errConfigurationTransition
	}
	return e.adapter.observeOwnedCompositeTemplateDeployment(ctx, e.config, e.source, e.device, e.target, op, time.Now())
}

var _ configurationExecutor = (*compositeTemplateExecutor)(nil)
