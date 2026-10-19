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

// This bridge consumes the existing shared all-member permit. It does not
// enable public preparation/dispatch, grant claims or supply convergence proof.
// Only the successful Prepared -> Submitting CAS caller can send one request;
// uncertain native acceptance or receipt persistence keeps the original fence.
func (a *adapter) dispatchWiredBulkProvision(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, permit *contract.GroupSubmissionPermit) (Task, error) {
	if a == nil || a.client == nil || config == nil || source == nil || permit == nil || !a.configurationGroupAdmissionReady() {
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
	dependencies, err := a.wiredBulkDependencies(ctx, source)
	if err != nil {
		return Task{}, err
	}
	batch, err := a.revalidateWiredBulkDispatch(ctx, config, source, op, "Prepared")
	if err != nil {
		return Task{}, err
	}
	var dispatch *contract.GroupDispatchPermit
	write, targets, err := a.client.planManagedWiredBulkProvision(ctx, a.statusReader, config.Namespace, batch.path, batch.members, func(ctx context.Context, _ []Device) error {
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
		if _, err := a.revalidateWiredBulkDispatch(ctx, config, source, current, "Submitting"); err != nil {
			return err
		}
		if err := dispatch.Check(ctx, time.Now()); err != nil {
			return err
		}
		// Authorization may itself observe changing source or ownership. Bracket
		// it with the same exact operation/native/managed identity checks.
		_, err = a.revalidateWiredBulkDispatch(ctx, config, source, current, "Submitting")
		return err
	}, dependencies...)
	if err != nil {
		return Task{}, err
	}
	if write == nil || write.resource != op.Resource || write.action != op.Action || write.identity.target != op.TargetID || write.identity.parent != op.ParentID || !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
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
	// Preserve a known native task even if the shared durable receipt CAS fails.
	if err := dispatch.RecordTask(ctx, task.ID, time.Now()); err != nil {
		return task, err
	}
	return task, nil
}

func (a *adapter) revalidateWiredBulkDispatch(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation, phase string) (wiredBulkBatch, error) {
	var empty wiredBulkBatch
	if op == nil || !supportedConfigurationApply(config) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" || (phase != "Prepared" && phase != "Submitting") || op.Phase != phase || op.TaskID != "" || op.CancellationRequestedAt != nil || (phase == "Prepared" && op.SubmittedAt != nil) || (phase == "Submitting" && (op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()))) {
		return empty, errConfigurationTransition
	}
	batch, err := a.retainedWiredBulkBatch(ctx, source, op)
	if err != nil {
		return empty, err
	}
	dependencies, err := a.wiredBulkDependencies(ctx, source)
	if err != nil {
		return empty, err
	}
	ok, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "inventory", func(ctx context.Context) (bool, error) {
		native, targets, err := a.client.wiredBulkTargets(ctx, a.statusReader, config.Namespace, batch.path, batch.members, dependencies...)
		if err != nil {
			return false, err
		}
		if native.mask == 0 || native.resource(native.mask) != op.Resource || native.anchor != op.TargetID || native.anchor != op.ParentID || !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
			return false, errConfigurationStale
		}
		return true, nil
	})
	if err != nil {
		return empty, err
	}
	if !ok {
		return empty, errConfigurationBusy
	}
	return batch, nil
}
