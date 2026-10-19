// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func (e *templateOwnedExecutor) findApplied(op *cvk.NetworkControllerConfigurationOperation) (templateCreateBinding, string, bool) {
	_, resource, stage, ok := templateApplyIdentity(op)
	if !ok {
		return templateCreateBinding{}, "", false
	}
	b, ok := e.bindings[resource]
	return b, stage, ok && e.comparison != nil && e.comparison.keyID == templateCreatorComparisonKey(resource)
}
func (e *templateOwnedExecutor) applyResource(legacy string) (string, error) {
	if e.comparison == nil {
		return "", nil
	}
	creators, err := templateCreatedRecords(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts})
	if err != nil {
		return "", err
	}
	for resource, b := range e.bindings {
		stage := "draft"
		match := b.legacy == legacy
		if b.kind == "p" {
			stage = "project"
		} else if strings.Replace(b.legacy, "draft:", "commit:", 1) == legacy {
			stage = "commit"
			match = true
		}
		if !match {
			continue
		}
		key, _ := templateCreateKey(resource)
		original, exists := creators[key]
		if !exists || templateCreatorComparisonKey(original.Operation.Resource) == "" {
			return "", nil
		}
		if templateCreatorComparisonKey(original.Operation.Resource) != e.comparison.keyID {
			return "", errConfigurationSecret
		}
		result := templateApplyResource(resource, stage)
		if stage == "commit" {
			result += ":" + templateVersionOwnershipTag
		}
		return result, nil
	}
	return "", nil
}
func (e *templateOwnedExecutor) appliedLegacy(op *cvk.NetworkControllerConfigurationOperation) (*cvk.NetworkControllerConfigurationOperation, bool) {
	b, stage, ok := e.findApplied(op)
	if !ok {
		return nil, false
	}
	copy := op.DeepCopy()
	copy.Resource = b.legacy
	if stage == "commit" {
		copy.Resource = strings.Replace(copy.Resource, "draft:", "commit:", 1)
	}
	return copy, true
}
func (e *templateOwnedExecutor) verifyApplied(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	legacy, ok := e.appliedLegacy(op)
	if !ok || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || !validAPIID(op.TaskID) || op.ConfigUID != e.owner || op.ConfigName != e.ownerName || op.ConfigGeneration != e.configGeneration || op.ControllerGeneration != e.controllerGeneration {
		return false, errConfigurationStale
	}
	if _, err := templateApplyCreator(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}, op); err != nil {
		return false, err
	}
	done, err := e.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
	if err != nil || !done {
		return false, err
	}
	if err := e.checkIdentities(ctx); err != nil {
		return false, err
	}
	done, err = e.base.verify(ctx, legacy)
	if err != nil || !done {
		return false, err
	}
	return e.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
}
func (e *templateOwnedExecutor) captureAppliedBaseline(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (string, error) {
	_, resource, _, ok := templateApplyIdentity(op)
	if !ok {
		return "", errConfigurationTransition
	}
	copy := op.DeepCopy()
	copy.Resource, copy.Action, copy.TargetID = resource, "Create", ""
	return e.captureTemplateBaseline(ctx, copy, op.TargetID, true)
}
