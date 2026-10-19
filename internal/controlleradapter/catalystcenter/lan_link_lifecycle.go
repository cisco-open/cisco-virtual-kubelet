// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"k8s.io/apimachinery/pkg/api/equality"
	"reflect"
	"time"
)

// Product observations vary; journal CAS, all-member dispatch and task retention
// have a single implementation. Strategies remain private to this adapter.
type lanLinkLifecycle struct {
	observe func(context.Context, *configapi.NetworkControllerConfig, *configSourceSnapshot) (map[string]lanLinkAddObservation, map[string]lanLinkIntent, error)
	plan    func(context.Context, *configapi.NetworkControllerConfig, *configSourceSnapshot, lanLinkIntent, func(context.Context) error) (*configurationWrite, error)
	valid   func(string, string) bool
}

func (a *adapter) lanLinkAddLifecycle() lanLinkLifecycle {
	return lanLinkLifecycle{a.observeLANLinkAddModel, func(ctx context.Context, config *configapi.NetworkControllerConfig, _ *configSourceSnapshot, want lanLinkIntent, guard func(context.Context) error) (*configurationWrite, error) {
		return a.client.planManagedLANLinkAdd(ctx, a.statusReader, config.Namespace, want, guard)
	}, validLANLinkAddResource}
}
func (a *adapter) prepareLANLinkAdd(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string) (*cvk.NetworkControllerConfigurationOperation, error) {
	return a.prepareLANLinkMutation(ctx, config, source, name, a.lanLinkAddLifecycle())
}
func (a *adapter) revalidateLANLinkAdd(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string, op *cvk.NetworkControllerConfigurationOperation, phase string) (map[string]lanLinkAddObservation, lanLinkIntent, error) {
	return a.revalidateLANLinkMutation(ctx, config, source, name, op, phase, a.lanLinkAddLifecycle())
}
func (a *adapter) dispatchLANLinkAdd(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string, permit *contract.GroupSubmissionPermit) (Task, error) {
	return a.dispatchLANLinkMutation(ctx, config, source, name, permit, a.lanLinkAddLifecycle())
}

// Prepare every declared link prerequisite before the existing journal CAS.
// Public Apply dispatches only after the shared all-member admission protocol.
func (a *adapter) prepareLANLinkMutation(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string, lifecycle lanLinkLifecycle) (*cvk.NetworkControllerConfigurationOperation, error) {
	if a == nil || a.client == nil || !a.configurationGroupAdmissionReady() {
		return nil, errConfigurationDeviceAdmission
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		return nil, errConfigurationBusy
	}
	before, wants, err := lifecycle.observe(ctx, config, source)
	if err != nil {
		return nil, err
	}
	want, exists := wants[name]
	if before[name].owned {
		return nil, errConfigurationOwned
	}
	if !exists {
		return nil, errConfigurationStale
	}
	write, err := lifecycle.plan(ctx, config, source, want, func(context.Context) error { return errConfigurationDeviceAdmission })
	if err != nil {
		return nil, err
	}
	if write.resource != before[name].resource || !reflect.DeepEqual(write.identity.groupTargets, before[name].targets) {
		return nil, errConfigurationStale
	}
	fresh, _, err := lifecycle.observe(ctx, config, source)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(before, fresh) {
		return nil, errConfigurationStale
	}
	current, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if current.UID != nc.UID || current.ResourceVersion != nc.ResourceVersion {
		return nil, errConfigurationStale
	}
	return a.prepareConfiguration(ctx, config, source, "lan_automation", write.resource, write.action, write.identity)
}

func (a *adapter) revalidateLANLinkMutation(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string, op *cvk.NetworkControllerConfigurationOperation, phase string, lifecycle lanLinkLifecycle) (map[string]lanLinkAddObservation, lanLinkIntent, error) {
	var want lanLinkIntent
	if op == nil || op.Phase != phase || (phase != "Prepared" && phase != "Submitting") || op.Section != "lan_automation" || !lifecycle.valid(op.Resource, op.Action) || op.TaskID != "" || op.CreatedResourceID != "" || op.CancellationRequestedAt != nil || contract.ValidateGroupJournalIdentity(op) != nil || (phase == "Prepared" && op.SubmittedAt != nil) || (phase == "Submitting" && (op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()))) {
		return nil, want, errConfigurationTransition
	}
	var observed map[string]lanLinkAddObservation
	ready, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "lan_automation", func(ctx context.Context) (bool, error) {
		var wants map[string]lanLinkIntent
		var err error
		observed, wants, err = lifecycle.observe(ctx, config, source)
		if err != nil {
			return false, err
		}
		selected, exists := observed[name]
		if !exists || selected.resource != op.Resource || !reflect.DeepEqual(selected.targets, op.PhysicalGroup.Targets) {
			return false, errConfigurationStale
		}
		want = wants[name]
		endpoints, err := a.client.lanLinkEndpointSnapshot(ctx, want)
		if err != nil {
			return false, err
		}
		if endpoints.primary.device.ID != op.TargetID || endpoints.secondary.device.ID != op.ParentID {
			return false, errConfigurationStale
		}
		return true, nil
	})
	if err != nil {
		return nil, want, err
	}
	if !ready {
		return nil, want, errConfigurationBusy
	}
	return observed, want, nil
}

func (a *adapter) dispatchLANLinkMutation(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string, permit *contract.GroupSubmissionPermit, lifecycle lanLinkLifecycle) (Task, error) {
	if a == nil || a.client == nil || permit == nil || !a.configurationGroupAdmissionReady() {
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
	baseline, want, err := a.revalidateLANLinkMutation(ctx, config, source, name, op, "Prepared", lifecycle)
	if err != nil {
		return Task{}, err
	}
	var dispatch *contract.GroupDispatchPermit
	write, err := lifecycle.plan(ctx, config, source, want, func(ctx context.Context) error {
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if current.Status.Configuration == nil || current.Status.Configuration.Operation == nil {
			return errConfigurationStale
		}
		actual := current.Status.Configuration.Operation
		expected := op.DeepCopy()
		expected.Phase = "Submitting"
		expected.SubmittedAt = actual.SubmittedAt
		if current.UID != nc.UID || !equality.Semantic.DeepEqual(actual, expected) {
			return errConfigurationStale
		}
		fresh, _, err := a.revalidateLANLinkMutation(ctx, config, source, name, actual, "Submitting", lifecycle)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(baseline, fresh) {
			return errConfigurationStale
		}
		return dispatch.Check(ctx, time.Now())
	})
	if err != nil {
		return Task{}, err
	}
	if write.resource != op.Resource || write.identity.target != op.TargetID || write.identity.parent != op.ParentID || !reflect.DeepEqual(write.identity.groupTargets, op.PhysicalGroup.Targets) {
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
