// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Bounded mark composition. REPLACE must not be
// partially executed as MARK; other inventory effects require explicit stages.
func (a *adapter) compileRMAMarkInventoryApply(config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	var zero configurationApplyPlan
	if a == nil || !a.configurationAdmissionReady() || config == nil || source == nil || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" || len(config.Spec.SecretRefs) != 0 {
		return zero, errConfigurationDeviceAdmission
	}
	inventory, _, actions, err := parseRMAInventory(source.raw)
	if err != nil {
		return zero, err
	}
	if len(actions) != len(inventory.devices) || len(inventory.tags) != 0 || len(inventory.discoveries) != 0 {
		return zero, errInvalidResponse
	}
	stages := &rmaInventoryStages{adapter: a, config: config, source: source}
	for _, device := range inventory.devices {
		want, ok := actions[device.reference.Name]
		if !ok || want.action != rmaMark || want.site == "" || device.reference.Serial == "" || device.role != "" || device.roleSource != "" || len(device.tags) != 0 || device.wireless != nil {
			return zero, errInvalidResponse
		}
		stages.entries = append(stages.entries, &deviceReplacementMarkExecutor{adapter: a, config: config, source: source, reference: device.reference, desired: want})
	}
	return configurationApplyPlan{section: "inventory", executor: stages, observeCompletion: stages.observeCompletion, recordCompletion: stages.recordCompletion, sections: []configapi.NetworkControllerSectionStatus{{Name: "inventory", ResourceCount: int64(stages.count())}}}, nil
}

// Mark and cleanup share all-target preflight and exact-operation routing.
type rmaInventoryEntry interface {
	configurationExecutor
	intent() (configurationDeviceReference, deviceReplacementIntent, error)
	validateSettled(*cvk.NetworkControllerConfigurationStatus, *deviceReplacementSnapshot) error
	observeCompletion(context.Context, *cvk.NetworkControllerConfigurationOperation, time.Time) (bool, error)
	recordCompletion(context.Context, *cvk.NetworkControllerConfigurationOperation) error
}

type rmaInventoryBinding struct{ device, serial, site, provision string }
type rmaInventoryStages struct {
	adapter  *adapter
	config   *configapi.NetworkControllerConfig
	source   *configSourceSnapshot
	entries  []rmaInventoryEntry
	bindings []rmaInventoryBinding
}

func (s *rmaInventoryStages) count() int { return len(s.entries) }
func (s *rmaInventoryStages) validate(ctx context.Context) error {
	if s == nil || s.adapter == nil || s.config == nil || len(s.entries) == 0 {
		return errConfigurationTransition
	}
	if err := s.adapter.checkConfigurationSnapshot(ctx, s.config, s.source); err != nil {
		return err
	}
	nc, err := s.adapter.currentController(ctx)
	if err != nil {
		return err
	}
	bindings := make([]rmaInventoryBinding, 0, len(s.entries))
	serials := map[string]bool{}
	for _, entry := range s.entries {
		if entry == nil {
			return errConfigurationTransition
		}
		ref, want, err := entry.intent()
		if err != nil {
			return err
		}
		snapshot, err := s.adapter.client.deviceReplacementSnapshot(ctx, ref, want)
		if err != nil {
			return err
		}
		if snapshot.siteID == "" || snapshot.provisionID == "" || serials[snapshot.serial] {
			return errConfigurationStale
		}
		serials[snapshot.serial] = true
		active := nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.TargetID == snapshot.target.ID && nc.Status.Configuration.Operation.TargetSerial == snapshot.serial
		if !active {
			if err := entry.validateSettled(nc.Status.Configuration, snapshot); err != nil {
				return err
			}
		}
		bindings = append(bindings, rmaInventoryBinding{snapshot.target.ID, snapshot.serial, snapshot.siteID, snapshot.provisionID})
	}
	if s.bindings != nil && !reflect.DeepEqual(s.bindings, bindings) {
		return errConfigurationStale
	}
	if err := s.adapter.checkConfigurationSnapshot(ctx, s.config, s.source); err != nil {
		return err
	}
	fresh, err := s.adapter.currentController(ctx)
	if err != nil {
		return err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
		return errConfigurationStale
	}
	s.bindings = bindings
	return nil
}
func (s *rmaInventoryStages) selected(op *cvk.NetworkControllerConfigurationOperation) (rmaInventoryEntry, error) {
	if op == nil || len(s.bindings) != len(s.entries) {
		return nil, errConfigurationTransition
	}
	var selected rmaInventoryEntry
	for i, binding := range s.bindings {
		if binding.device == op.TargetID && binding.serial == op.TargetSerial && binding.site == op.ParentID {
			if selected != nil {
				return nil, errConfigurationOwned
			}
			selected = s.entries[i]
		}
	}
	if selected == nil {
		return nil, errConfigurationStale
	}
	return selected, nil
}
func (s *rmaInventoryStages) next(ctx context.Context) (*configurationWrite, error) {
	if err := s.validate(ctx); err != nil {
		return nil, err
	}
	nc, err := s.adapter.currentController(ctx)
	if err != nil {
		return nil, err
	}
	entries := s.entries
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		selected, err := s.selected(nc.Status.Configuration.Operation)
		if err != nil {
			return nil, err
		}
		entries = []rmaInventoryEntry{selected}
	}
	for _, entry := range entries {
		write, err := entry.next(ctx)
		if err != nil {
			return nil, err
		}
		if write == nil {
			continue
		}
		if write.submit == nil || write.submitCompleted != nil || write.submitCreated != nil {
			return nil, errConfigurationTransition
		}
		guarded := *write
		guarded.submit = func(ctx context.Context) (Task, error) {
			if err := s.validate(ctx); err != nil {
				return Task{}, err
			}
			return write.submit(ctx)
		}
		return &guarded, nil
	}
	return nil, nil
}
func (s *rmaInventoryStages) observeCompletion(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	if err := s.validate(ctx); err != nil {
		return false, err
	}
	selected, err := s.selected(op)
	if err != nil {
		return false, err
	}
	return selected.observeCompletion(ctx, op, now)
}
func (s *rmaInventoryStages) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if err := s.validate(ctx); err != nil {
		return false, err
	}
	selected, err := s.selected(op)
	if err != nil {
		return false, err
	}
	return selected.verify(ctx, op)
}
func (s *rmaInventoryStages) recordCompletion(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) error {
	if err := s.validate(ctx); err != nil {
		return err
	}
	selected, err := s.selected(op)
	if err != nil {
		return err
	}
	return selected.recordCompletion(ctx, op)
}

var _ configurationExecutor = (*rmaInventoryStages)(nil)
