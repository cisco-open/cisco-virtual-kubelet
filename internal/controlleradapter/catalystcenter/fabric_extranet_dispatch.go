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

// A real all-member submission permit is mandatory. The native primitives are
// unchanged; this bridge owns exact journal/source/group revalidation around
// the shared one-shot transition and durable task acknowledgement.
func (a *adapter) dispatchExtranetPolicy(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string, permit *contract.GroupSubmissionPermit) (Task, error) {
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
	if err := a.revalidateExtranetOperation(ctx, config, source, name, op, "Prepared"); err != nil {
		return Task{}, err
	}
	want, err := declaredExtranetPolicy(source, name)
	if err != nil {
		return Task{}, err
	}
	var dispatch *contract.GroupDispatchPermit
	guard := func(ctx context.Context) error {
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
		if err := a.revalidateExtranetOperation(ctx, config, source, name, current, "Submitting"); err != nil {
			return err
		}
		return dispatch.Check(ctx, time.Now())
	}
	var plan *configurationWrite
	nativeResource := op.Resource
	if op.Action == "Create" {
		plan, err = a.client.planGuardedExtranetCreate(ctx, want, guard)
	} else {
		nativeResource, err = extranetUpdateNativeResource(op.Resource)
		if err == nil {
			plan, err = a.planOwnedExtranetExpansion(ctx, config, source, want)
			if err == nil && plan != nil {
				plan = guardExtranetWrite(plan, guard)
			}
		}
	}
	if err != nil {
		return Task{}, err
	}
	if plan == nil || plan.action != op.Action || plan.resource != nativeResource || plan.identity.target != op.TargetID || plan.identity.parent != op.ParentID {
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
