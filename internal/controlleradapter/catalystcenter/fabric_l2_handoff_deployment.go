// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"strings"
	"sync/atomic"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Reuse fabric-device placement, role, provisioning-record and membership
// validation. Native provisioning record existence is not task completion; the
// manifest coordinator must first finish any requested provision/reprovision.
func (c *client) fabricL2HandoffPrerequisites(ctx context.Context, want fabricL2HandoffDeploymentIntent) (*fabricDeviceDependencies, error) {
	if want.device.state != "PROVISION" && want.device.state != "REPROVISION" {
		return nil, errInvalidResponse
	}
	if want.handoff.device != want.device.inventory.reference.Name {
		return nil, errInvalidResponse
	}
	deps, err := c.fabricDeviceDependencies(ctx, want.device)
	if err != nil {
		return nil, err
	}
	actual, exists := deps.before[deps.target.ID]
	if !exists || !fabricDeviceConfigurationMatches(deps.native, actual) {
		return nil, errConfigurationBusy
	}
	return deps, nil
}

// Private composition boundary. Physical admission and durable journal ownership
// are still mandatory at the adapter; this function provides no dispatch permit.
func (c *client) planFabricL2HandoffDeployment(ctx context.Context, want fabricL2HandoffDeploymentIntent) (*configurationWrite, error) {
	return c.planGuardedFabricL2HandoffDeployment(ctx, want, nil)
}

func (c *client) planGuardedFabricL2HandoffDeployment(ctx context.Context, want fabricL2HandoffDeploymentIntent, guard func(context.Context) error) (*configurationWrite, error) {
	deps, err := c.fabricL2HandoffPrerequisites(ctx, want)
	if err != nil {
		return nil, err
	}
	want.device.roles = deps.native.Roles
	want.device.border = deps.native.Border
	native, err := c.planGuardedFabricL2HandoffCreate(ctx, want.handoff, want.device.inventory.reference, want.device.fabricSite, guard)
	if err != nil || native == nil {
		return native, err
	}
	write := *native
	write.resource = native.resource + ":" + deps.digest
	var used atomic.Bool
	write.submit = func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		fresh, err := c.fabricL2HandoffPrerequisites(ctx, want)
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

func (c *client) verifyFabricL2HandoffDeployment(ctx context.Context, want fabricL2HandoffDeploymentIntent, op *cvk.NetworkControllerConfigurationOperation) (string, error) {
	if op == nil || op.Phase != "Verifying" || !validAPIID(op.TaskID) {
		return "", errConfigurationTransition
	}
	deps, err := c.fabricL2HandoffPrerequisites(ctx, want)
	if err != nil {
		return "", err
	}
	suffix := ":" + deps.digest
	if !strings.HasSuffix(op.Resource, suffix) {
		return "", errConfigurationStale
	}
	native := op.DeepCopy()
	native.Resource = strings.TrimSuffix(native.Resource, suffix)
	return c.verifyFabricL2HandoffCreate(ctx, want.handoff, want.device.inventory.reference, want.device.fabricSite, native)
}
