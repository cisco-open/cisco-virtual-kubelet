// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"k8s.io/apimachinery/pkg/api/equality"
)

func (a *adapter) revalidateFabricDevicesBulk(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, wants []fabricDeviceIntent, op *cvk.NetworkControllerConfigurationOperation, phase string) error {
	if op == nil || (phase != "Prepared" && phase != "Submitting") || op.Phase != phase || op.Section != "fabric" || !validConfigurationGroupAction(op.Section, op.Resource, op.Action) || op.TaskID != "" || op.CreatedResourceID != "" || op.CancellationRequestedAt != nil || (phase == "Prepared" && op.SubmittedAt != nil) || (phase == "Submitting" && (op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()))) || contract.ValidateGroupJournalIdentity(op) != nil {
		return errConfigurationTransition
	}
	if err := declaredFabricDevicesBulk(source, wants); err != nil {
		return err
	}
	ok, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
		write, targets, err := a.client.planManagedFabricDevicesBulk(ctx, a.statusReader, config.Namespace, wants, func(context.Context, []Device) error { return errConfigurationDeviceAdmission })
		if err != nil {
			return false, err
		}
		if write == nil || write.resource != op.Resource || write.action != op.Action || write.identity.target != op.TargetID || write.identity.parent != op.ParentID || !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
			return false, errConfigurationStale
		}
		return true, nil
	})
	if err != nil {
		return err
	}
	if !ok {
		return errConfigurationBusy
	}
	return nil
}

// Only the shared non-reconstructible all-member permit can cross Submitting.
// Native acknowledgement is retained through its existing task-receipt CAS.
func (a *adapter) dispatchFabricDevicesBulk(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, wants []fabricDeviceIntent, permit *contract.GroupSubmissionPermit) (Task, error) {
	if a == nil || a.client == nil || config == nil || permit == nil || !a.configurationGroupAdmissionReady() {
		return Task{}, errConfigurationDeviceAdmission
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return Task{}, err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return Task{}, errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	if err := a.revalidateFabricDevicesBulk(ctx, config, source, wants, op, "Prepared"); err != nil {
		return Task{}, err
	}
	var dispatch *contract.GroupDispatchPermit
	write, targets, err := a.client.planManagedFabricDevicesBulk(ctx, a.statusReader, config.Namespace, wants, func(ctx context.Context, _ []Device) error {
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
		if err := a.revalidateFabricDevicesBulk(ctx, config, source, wants, current, "Submitting"); err != nil {
			return err
		}
		if err := dispatch.Check(ctx, time.Now()); err != nil {
			return err
		}
		return a.revalidateFabricDevicesBulk(ctx, config, source, wants, current, "Submitting")
	})
	if err != nil {
		return Task{}, err
	}
	if write == nil || write.resource != op.Resource || write.identity.target != op.TargetID || write.identity.parent != op.ParentID || !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
		return Task{}, errConfigurationStale
	}
	dispatch, err = permit.BeginSubmissionFor(ctx, nc, time.Now())
	if err != nil {
		return Task{}, err
	}
	task, err := write.submit(ctx)
	if err != nil {
		return Task{}, err
	}
	if err := dispatch.RecordTask(ctx, task.ID, time.Now()); err != nil {
		return task, err
	}
	return task, nil
}
