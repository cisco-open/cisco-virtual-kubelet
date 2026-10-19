// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"strings"
)

// Preserve existing site/zone journal identities and preflight child conflicts
// before provisioning a parent. L2 parents precede inherited zone networks.
type fabricSiteL2Executor struct {
	parents      configurationExecutor
	parentReport func(context.Context) ([]configapi.NetworkControllerDriftEntry, error)
	networks     *fabricL2Executor
}

func (e *fabricSiteL2Executor) count() int { return e.parents.count() + e.networks.count() }
func (e *fabricSiteL2Executor) next(ctx context.Context) (*configurationWrite, error) {
	if err := e.networks.preflight(ctx); err != nil {
		return nil, err
	}
	write, err := e.parents.next(ctx)
	if err != nil || write != nil {
		return write, err
	}
	return e.networks.next(ctx)
}
func (e *fabricSiteL2Executor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil {
		return false, errConfigurationTransition
	}
	if strings.HasPrefix(op.Resource, "fabric-l2:") {
		// Parent topology/authentication must still converge at readback. This
		// also catches a newly inserted intermediate fabric site or a replaced
		// hierarchy object, through the existing parent executor validation.
		pending, err := e.parents.next(ctx)
		if err != nil {
			return false, err
		}
		if pending != nil {
			return false, errConfigurationStale
		}
		return e.networks.verify(ctx, op)
	}
	return e.parents.verify(ctx, op)
}
func (e *fabricSiteL2Executor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	parents, err := e.parentReport(ctx)
	if err != nil {
		return nil, err
	}
	networks, err := e.networks.report(ctx)
	if err != nil {
		return nil, err
	}
	return append(parents, networks...), nil
}
