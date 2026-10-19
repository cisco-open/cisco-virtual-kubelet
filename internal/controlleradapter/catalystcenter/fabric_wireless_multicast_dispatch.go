// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"k8s.io/apimachinery/pkg/api/equality"
)

// Dispatch requires a real all-member claim permit; callers cannot substitute an
// authorization callback. Public reconciliation must establish group admission
// before invoking this bridge. Any uncertainty retains the shared journal and
// reservations; no native retry or automatic release occurs here.
func (a *adapter) dispatchFabricWirelessMulticast(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, path string, permit *contract.GroupSubmissionPermit) (Task, error) {
	if a == nil || a.client == nil || config == nil || permit == nil {
		return Task{}, errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return Task{}, err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return Task{}, errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	if err := a.revalidateFabricWirelessMulticastOperation(ctx, config, source, path, op, "Prepared"); err != nil {
		return Task{}, err
	}
	want, err := a.declaredFabricWirelessMulticast(ctx, source, path)
	if err != nil {
		return Task{}, err
	}
	var dispatch *contract.GroupDispatchPermit
	plan, err := a.client.planGuardedFabricWirelessMulticast(ctx, path, want, func(ctx context.Context) error {
		fresh, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if fresh.Status.Configuration == nil || fresh.Status.Configuration.Operation == nil {
			return errConfigurationStale
		}
		current := fresh.Status.Configuration.Operation
		expected := op.DeepCopy()
		expected.Phase = "Submitting"
		expected.SubmittedAt = current.SubmittedAt
		if fresh.UID != nc.UID || !equality.Semantic.DeepEqual(expected, current) {
			return errConfigurationStale
		}
		if err := a.revalidateFabricWirelessMulticastOperation(ctx, config, source, path, current, "Submitting"); err != nil {
			return err
		}
		return dispatch.Check(ctx, time.Now())
	})
	if err != nil {
		return Task{}, err
	}
	if plan == nil || plan.resource != op.Resource || plan.identity.target != op.TargetID || plan.identity.parent != op.ParentID {
		return Task{}, errConfigurationStale
	}
	dispatch, err = permit.BeginSubmissionFor(ctx, nc, time.Now())
	if err != nil {
		return Task{}, err
	}
	task, err := plan.submit(ctx)
	if err != nil {
		return Task{}, err
	}
	// Return the known task even if its durable receipt is uncertain. The error
	// forbids treating acceptance as committed or releasing the device group.
	if err := dispatch.RecordTask(ctx, task.ID, time.Now()); err != nil {
		return task, err
	}
	return task, nil
}
