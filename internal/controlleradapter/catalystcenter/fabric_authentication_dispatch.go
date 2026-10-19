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

// This bridge requires the shared all-member permit. It does not implement a
// competing claim, retry or task loop, nor does it release uncertain operations.
func (a *adapter) dispatchFabricAuthentication(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, path string, permit *contract.GroupSubmissionPermit) (Task, error) {
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
	if err := a.revalidateFabricAuthenticationOperation(ctx, config, source, path, op, "Prepared"); err != nil {
		return Task{}, err
	}
	want, err := a.declaredFabricAuthentication(ctx, source, path)
	if err != nil {
		return Task{}, err
	}
	var dispatch *contract.GroupDispatchPermit
	plan, err := a.client.planFabricAuthenticationWithDispatch(ctx, []fabricAuthIntent{want}, func(ctx context.Context) error {
		fresh, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if fresh.Status.Configuration == nil || fresh.Status.Configuration.Operation == nil {
			return errConfigurationStale
		}
		current := fresh.Status.Configuration.Operation
		expected := op.DeepCopy()
		phase := "Prepared"
		if dispatch != nil {
			phase = "Submitting"
			expected.Phase, expected.SubmittedAt = phase, current.SubmittedAt
		}
		if fresh.UID != nc.UID || !equality.Semantic.DeepEqual(expected, current) {
			return errConfigurationStale
		}
		if err := a.revalidateFabricAuthenticationOperation(ctx, config, source, path, current, phase); err != nil {
			return err
		}
		// The core invokes this guard while observing a plan as well as before
		// dispatch. Observation in Prepared grants no submission capability; the
		// only plan.submit below is after BeginSubmissionFor succeeds.
		return nil
	}, func(ctx context.Context) error {
		if dispatch == nil {
			return errConfigurationDeviceAdmission
		}
		return dispatch.Check(ctx, time.Now())
	})
	if err != nil {
		return Task{}, err
	}
	if plan == nil || plan.identity.target != op.TargetID || plan.identity.parent != op.ParentID {
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
	if err := dispatch.RecordTask(ctx, task.ID, time.Now()); err != nil {
		return task, err
	}
	return task, nil
}
