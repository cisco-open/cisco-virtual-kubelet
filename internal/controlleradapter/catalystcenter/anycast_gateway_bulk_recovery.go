// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"strings"
	"time"
)

type anycastBulkRecoveryScope struct {
	prefix   string
	executor *anycastGatewayExecutor
	cohorts  [][]fabricAnycastIntent
}

func (s anycastBulkRecoveryScope) selects(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil || op.Section != "fabric" || op.Phase != "Unknown" || op.Action != "Create" || !configurationOperationID.MatchString(op.ID) || op.ConfigUID == "" || op.ConfigName == "" || op.ConfigGeneration < 1 || op.ControllerGeneration < 1 || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.TargetID != "" || !validAPIID(op.ParentID) || op.TargetSerial != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CreatedResourceID != "" || op.PreparedAt != nil || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 || len(op.SSIDCredentialHistory) != 0 || (op.SourceUID == "") != (op.SourceVersion == "") {
		return false
	}
	if !strings.HasPrefix(op.Resource, s.prefix) {
		return false
	}
	receipt, err := decodeAnycastBulkReceipt(strings.TrimPrefix(op.Resource, s.prefix))
	if err != nil {
		return false
	}
	matches := 0
	for _, cohort := range s.cohorts {
		if len(cohort) != receipt.total {
			continue
		}
		if receipt.version == 2 {
			digest, err := anycastCohortDigest(cohort)
			if err != nil || digest != receipt.cohort {
				continue
			}
		}
		matches++
	}
	// A legacy receipt has no pure source digest, so the exact native parent and
	// original binding are checked by the observer before reading its task tree.
	return matches > 0
}

func (c *client) bindAnycastBulkTaskRecovery(plan *configurationApplyPlan) {
	if plan == nil || plan.section != "fabric" {
		return
	}
	var scopes []anycastBulkRecoveryScope
	visitAnycastGateways(plan.executor, "", func(e *anycastGatewayExecutor, prefix string) {
		groups := map[string][]fabricAnycastIntent{}
		for _, want := range e.desired {
			groups[want.path+"\x00"+want.parent] = append(groups[want.path+"\x00"+want.parent], want)
		}
		if len(groups) == 0 {
			return
		}
		scope := anycastBulkRecoveryScope{prefix: prefix, executor: e}
		for _, members := range groups {
			scope.cohorts = append(scope.cohorts, members)
		}
		scopes = append(scopes, scope)
	})
	if len(scopes) == 0 {
		return
	}
	lookup := func(op *cvk.NetworkControllerConfigurationOperation) (*anycastBulkRecoveryScope, bool) {
		if op == nil {
			return nil, false
		}
		for i := range scopes {
			s := &scopes[i]
			if strings.HasPrefix(op.Resource, s.prefix) && isAnycastBulkResource(strings.TrimPrefix(op.Resource, s.prefix)) {
				return s, true
			}
		}
		return nil, false
	}
	priorRecover, priorObserve := plan.recoverKnownTask, plan.observeCompletion
	plan.recoverKnownTask = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		if scope, recognized := lookup(op); recognized {
			return scope.selects(op)
		}
		return priorRecover != nil && priorRecover(op)
	}
	plan.observeCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
		if scope, recognized := lookup(op); recognized {
			observation := op.DeepCopy()
			if observation.Phase == "Accepted" {
				observation.Phase = "Unknown"
			}
			if !scope.selects(observation) {
				return false, errConfigurationTransition
			}
			local := observation.DeepCopy()
			local.Resource = strings.TrimPrefix(local.Resource, scope.prefix)
			// This is read-only validation of the original parent, mask, source cohort,
			// resolved dependencies and preexisting UUIDs. Pending members may wait;
			// they never authorize resubmitting a smaller replacement batch.
			if _, err := scope.executor.verify(ctx, local); err != nil {
				return false, err
			}
			return c.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		}
		if priorObserve != nil {
			return priorObserve(ctx, op, now)
		}
		return c.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	}
}
