// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func boundFabricDeviceResource(deps *fabricDeviceDependencies) string {
	return fabricDeviceCreateResource(deps.native) + ":" + deps.recoveryDigest
}

// Staged adapter executor: dependency-bound native submission and recovery are
// separate from the still-required provisioning, group admission and journal
// coordinator. No caller may treat this primitive as a dispatch permit.
func (c *client) planBoundFabricDeviceCreate(ctx context.Context, want fabricDeviceIntent) (*configurationWrite, error) {
	deps, err := c.fabricDeviceDependencies(ctx, want)
	if err != nil {
		return nil, err
	}
	// Snapshot mutable model fields before constructing the submit closure.
	want.roles = deps.native.Roles
	want.border = deps.native.Border
	native, err := c.planFabricDeviceCreate(ctx, deps.native)
	if err != nil {
		return nil, err
	}
	if native == nil {
		if _, existed := deps.before[deps.target.ID]; !existed {
			return nil, errConfigurationStale
		}
		return nil, nil
	}
	write := *native
	write.resource = boundFabricDeviceResource(deps)
	write.submit = func(ctx context.Context) (Task, error) {
		fresh, err := c.fabricDeviceDependencies(ctx, want)
		if err != nil {
			return Task{}, err
		}
		if fresh.digest != deps.digest {
			return Task{}, errConfigurationStale
		}
		return native.submit(ctx)
	}
	return &write, nil
}

func (c *client) verifyBoundFabricDeviceCreate(ctx context.Context, want fabricDeviceIntent, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	// Missing receipt / Unknown submission cannot become success through readback.
	// The journal coordinator must also have verified the asynchronous task.
	if op == nil || op.Phase != "Verifying" || !validAPIID(op.TaskID) || op.Action != "Create" || op.TargetSerial != "" || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	deps, err := c.fabricDeviceDependencies(ctx, want)
	if err != nil {
		return false, err
	}
	if op.Resource != boundFabricDeviceResource(deps) || op.TargetID != deps.target.ID || op.ParentID != deps.native.FabricID {
		return false, errConfigurationStale
	}
	got, exists := deps.before[deps.target.ID]
	return exists && fabricDeviceConfigurationMatches(deps.native, got), nil
}
