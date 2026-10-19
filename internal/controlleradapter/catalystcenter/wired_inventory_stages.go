// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	"strings"
)

type wiredInventoryStages struct {
	adapter       *adapter
	prerequisites configurationExecutor
	provisioning  []*wiredProvisionExecutor
	validate      func(context.Context) error
}

func (s *wiredInventoryStages) count() int {
	n := 0
	if s.prerequisites != nil {
		n = s.prerequisites.count()
	}
	return n + len(s.provisioning)
}
func (s *wiredInventoryStages) selected(op *cvk.NetworkControllerConfigurationOperation) (*wiredProvisionExecutor, error) {
	if op == nil || !validWiredProvisionResource(op.Resource) {
		return nil, errConfigurationTransition
	}
	serial, err := topologyidentity.CanonicalPhysicalIdentity(op.TargetSerial)
	if err != nil || serial != op.TargetSerial {
		return nil, errConfigurationStale
	}
	var selected *wiredProvisionExecutor
	for _, executor := range s.provisioning {
		if executor == nil {
			return nil, errConfigurationTransition
		}
		declared, err := topologyidentity.CanonicalPhysicalIdentity(executor.desired.reference.Serial)
		if err != nil {
			return nil, err
		}
		if declared == serial {
			if selected != nil {
				return nil, errConfigurationOwned
			}
			selected = executor
		}
	}
	if selected == nil {
		return nil, errConfigurationStale
	}
	return selected, nil
}
func (s *wiredInventoryStages) guarded(write *configurationWrite) *configurationWrite {
	if write == nil {
		return nil
	}
	out := *write
	out.submit = func(ctx context.Context) (Task, error) {
		if err := s.validate(ctx); err != nil {
			return Task{}, err
		}
		return write.submit(ctx)
	}
	return &out
}
func (s *wiredInventoryStages) next(ctx context.Context) (*configurationWrite, error) {
	if s.adapter == nil || s.prerequisites == nil || s.validate == nil || len(s.provisioning) == 0 {
		return nil, errConfigurationTransition
	}
	for _, executor := range s.provisioning {
		if executor == nil {
			return nil, errConfigurationTransition
		}
	}
	if err := s.validate(ctx); err != nil {
		return nil, err
	}
	write, err := s.prerequisites.next(ctx)
	if err != nil {
		return nil, err
	}
	if write != nil {
		current, err := s.adapter.currentController(ctx)
		if err != nil {
			return nil, err
		}
		if current.Status.Configuration != nil && current.Status.Configuration.Operation != nil && strings.HasPrefix(current.Status.Configuration.Operation.Resource, "wired-provision:") {
			return nil, errConfigurationBusy
		}
		return s.guarded(write), nil
	}
	nc, err := s.adapter.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		executor, err := s.selected(nc.Status.Configuration.Operation)
		if err != nil {
			return nil, err
		}
		write, err := executor.next(ctx)
		return s.guarded(write), err
	}
	for _, executor := range s.provisioning {
		write, err := executor.next(ctx)
		if err != nil || write != nil {
			return s.guarded(write), err
		}
	}
	return nil, nil
}
func (s *wiredInventoryStages) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || s.prerequisites == nil || s.validate == nil {
		return false, errConfigurationTransition
	}
	if err := s.validate(ctx); err != nil {
		return false, err
	}
	if !strings.HasPrefix(op.Resource, "wired-provision:") {
		return s.prerequisites.verify(ctx, op)
	}
	executor, err := s.selected(op)
	if err != nil {
		return false, err
	}
	pending, err := s.prerequisites.next(ctx)
	if err != nil {
		return false, err
	}
	if pending != nil {
		return false, errConfigurationBusy
	}
	return executor.verify(ctx, op)
}

var _ configurationExecutor = (*wiredInventoryStages)(nil)
