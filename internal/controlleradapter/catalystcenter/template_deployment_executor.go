// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"reflect"
	"time"
)

// Uses the existing executor and journal contracts. The enclosing inventory
// stage must order provisioning and obtain durable physical admission.
type regularTemplateExecutor struct {
	adapter *adapter
	config  *configapi.NetworkControllerConfig
	source  *configSourceSnapshot
	device  inventoryDeviceIntent
	target  regularTemplateTarget
}

func (*regularTemplateExecutor) count() int { return 1 }
func (e *regularTemplateExecutor) next(ctx context.Context) (*configurationWrite, error) {
	if e == nil {
		return nil, errConfigurationTransition
	}
	return nextPreparedInventoryWrite(ctx, e.adapter, e.config, e.source, func(ctx context.Context) (*configurationWrite, error) {
		return e.adapter.planOwnedTemplateDeployment(ctx, e.config, e.source, e.device, e.target)
	}, func(ctx context.Context) (*configurationWrite, error) {
		return e.adapter.client.planRegularTemplateDeployment(ctx, e.device, e.target)
	})
}

// Shared exact Prepared resume. Submission remains behind journal admission;
// Accepted/Verifying/Unknown operations can never return a write here.
func nextPreparedInventoryWrite(ctx context.Context, a *adapter, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, ownedPlan, nativePlan func(context.Context) (*configurationWrite, error)) (*configurationWrite, error) {
	if a == nil || config == nil || nativePlan == nil || ownedPlan == nil || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" || len(config.Spec.SecretRefs) != 0 {
		return nil, errConfigurationTransition
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return ownedPlan(ctx)
	}
	op := nc.Status.Configuration.Operation
	uid, version := "", ""
	if source != nil {
		uid = string(source.uid)
		version = source.version
	}
	if op.Phase != "Prepared" || op.TaskID != "" || op.SubmittedAt != nil || op.PhysicalGroup != nil || op.CancellationRequestedAt != nil || op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.Section != "inventory" || op.SourceUID != uid || op.SourceVersion != version || len(op.CredentialVersions) != 0 {
		return nil, errConfigurationBusy
	}
	owners := 0
	for _, owner := range nc.Status.Configuration.Owners {
		if owner.Section == "inventory" {
			if owner.ConfigUID != op.ConfigUID || owner.ConfigName != op.ConfigName {
				return nil, errConfigurationOwned
			}
			owners++
		}
	}
	if owners != 1 {
		return nil, errConfigurationOwned
	}
	write, err := nativePlan(ctx)
	if err != nil {
		return nil, err
	}
	if write == nil || !reflect.DeepEqual(write.identity.dependencies, op.DependencyVersions) || write.resource != op.Resource || write.action != op.Action || write.identity.target != op.TargetID || write.identity.parent != op.ParentID || write.identity.serial != op.TargetSerial {
		return nil, errConfigurationStale
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
		return nil, errConfigurationStale
	}
	return write, nil
}

func (e *regularTemplateExecutor) observeCompletion(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	if e == nil || e.adapter == nil || op == nil || op.Phase != "Accepted" {
		return false, errConfigurationTransition
	}
	return e.adapter.observeOwnedTemplateDeployment(ctx, e.config, e.source, e.device, e.target, op, now)
}
func (e *regularTemplateExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if e == nil || e.adapter == nil || op == nil || op.Phase != "Verifying" {
		return false, errConfigurationTransition
	}
	return e.adapter.observeOwnedTemplateDeployment(ctx, e.config, e.source, e.device, e.target, op, time.Now())
}

var _ configurationExecutor = (*regularTemplateExecutor)(nil)
