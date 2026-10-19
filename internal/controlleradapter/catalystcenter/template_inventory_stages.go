// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"strings"
	"time"
)

type templateDeploymentExecutor interface {
	configurationExecutor
	observeCompletion(context.Context, *cvk.NetworkControllerConfigurationOperation, time.Time) (bool, error)
	deploymentKind() string
}

func (*regularTemplateExecutor) deploymentKind() string   { return "regular" }
func (*compositeTemplateExecutor) deploymentKind() string { return "composite" }
func isTemplateDeploymentResource(resource string) bool {
	return strings.HasPrefix(resource, "template-deploy:") || strings.HasPrefix(resource, "template-composite-deploy:")
}

type templateStageBinding struct {
	executor                 templateDeploymentExecutor
	deviceID, serial, baseID string
}

// Compose existing provisioning with deployments. Bindings and validate must
// come from compiler preflight; this wrapper never invents physical identities.
type templateInventoryStages struct {
	adapter       *adapter
	prerequisites configurationApplyPlan
	templates     []templateStageBinding
	validate      func(context.Context) error
	settle        func(context.Context, *configurationWrite) error
}

func (s *templateInventoryStages) count() int {
	return s.prerequisites.executor.count() + len(s.templates)
}
func (s *templateInventoryStages) selected(op *cvk.NetworkControllerConfigurationOperation) (templateDeploymentExecutor, error) {
	if op == nil {
		return nil, errConfigurationTransition
	}
	binding, err := templateReceiptBinding(op)
	if err != nil {
		return nil, err
	}
	var selected templateDeploymentExecutor
	for _, entry := range s.templates {
		if entry.executor == nil || !validAPIID(entry.baseID) || !validAPIID(entry.deviceID) || entry.serial == "" {
			return nil, errConfigurationTransition
		}
		if entry.executor.deploymentKind() == binding.kind && entry.baseID == binding.baseID && entry.deviceID == op.TargetID && entry.serial == op.TargetSerial {
			if selected != nil {
				return nil, errConfigurationOwned
			}
			selected = entry.executor
		}
	}
	if selected == nil {
		return nil, errConfigurationStale
	}
	return selected, nil
}
func (s *templateInventoryStages) guarded(ctx context.Context, write *configurationWrite) (*configurationWrite, error) {
	if write == nil {
		return nil, nil
	}
	if write.submitCreated != nil || (write.submit == nil) == (write.submitCompleted == nil) {
		return nil, errConfigurationTransition
	}
	settle := func(ctx context.Context) error {
		if isTemplateDeploymentResource(write.resource) && s.settle != nil {
			return s.settle(ctx, write)
		}
		return nil
	}
	// Planning without a journal may establish the persisted observation floor.
	// Prepared resume must recheck it before returning a dispatchable write.
	if err := settle(ctx); err != nil {
		return nil, err
	}
	copy := *write
	if isTemplateDeploymentResource(write.resource) && s.settle != nil {
		copy.identity.preparationWait = true
	}
	if write.submit != nil {
		copy.submit = func(ctx context.Context) (Task, error) {
			if err := s.validate(ctx); err != nil {
				return Task{}, err
			}
			if err := settle(ctx); err != nil {
				return Task{}, err
			}
			return write.submit(ctx)
		}
	} else {
		copy.submitCompleted = func(ctx context.Context) error {
			if err := s.validate(ctx); err != nil {
				return err
			}
			if err := settle(ctx); err != nil {
				return err
			}
			return write.submitCompleted(ctx)
		}
	}
	return &copy, nil
}
func (s *templateInventoryStages) next(ctx context.Context) (*configurationWrite, error) {
	if s.adapter == nil || s.prerequisites.executor == nil || s.validate == nil || len(s.templates) == 0 {
		return nil, errConfigurationTransition
	}
	if err := s.validate(ctx); err != nil {
		return nil, err
	}
	nc, err := s.adapter.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		op := nc.Status.Configuration.Operation
		executor := s.prerequisites.executor
		if isTemplateDeploymentResource(op.Resource) {
			selected, err := s.selected(op)
			if err != nil {
				return nil, err
			}
			executor = selected
		}
		write, err := executor.next(ctx)
		if err != nil {
			return nil, err
		}
		return s.guarded(ctx, write)
	}
	write, err := s.prerequisites.executor.next(ctx)
	if err != nil {
		return nil, err
	}
	if write != nil {
		return s.guarded(ctx, write)
	}
	for _, entry := range s.templates {
		if entry.executor == nil {
			return nil, errConfigurationTransition
		}
		write, err := entry.executor.next(ctx)
		if err != nil {
			return nil, err
		}
		if write != nil {
			return s.guarded(ctx, write)
		}
	}
	return nil, nil
}
func (s *templateInventoryStages) observeCompletion(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	if s.adapter == nil || s.validate == nil || op == nil {
		return false, errConfigurationTransition
	}
	if err := s.validate(ctx); err != nil {
		return false, err
	}
	if !isTemplateDeploymentResource(op.Resource) {
		return s.prerequisites.taskComplete(ctx, s.adapter.client, op, now)
	}
	executor, err := s.selected(op)
	if err != nil {
		return false, err
	}
	return executor.observeCompletion(ctx, op, now)
}
func (s *templateInventoryStages) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if s.validate == nil || s.prerequisites.executor == nil || op == nil {
		return false, errConfigurationTransition
	}
	if err := s.validate(ctx); err != nil {
		return false, err
	}
	if !isTemplateDeploymentResource(op.Resource) {
		return s.prerequisites.executor.verify(ctx, op)
	}
	executor, err := s.selected(op)
	if err != nil {
		return false, err
	}
	return executor.verify(ctx, op)
}

var _ configurationExecutor = (*templateInventoryStages)(nil)
