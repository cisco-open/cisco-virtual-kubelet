// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func siteCredentialTaskScope(e *siteCredentialExecutor, op *cvk.NetworkControllerConfigurationOperation, now time.Time) bool {
	if e == nil || op == nil || op.Section != "network_settings" || op.Action != "Update" || (op.Phase != "Accepted" && op.Phase != "Unknown" && op.Phase != "Verifying") || !configurationOperationID.MatchString(op.ID) || op.ConfigUID == "" || op.ConfigName == "" || op.ConfigGeneration < 1 || op.ControllerGeneration < 1 || !validAPIID(op.TargetID) || !validAPIID(op.ParentID) || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(now) || op.TargetSerial != "" || op.CreatedResourceID != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.PreparedAt != nil || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 || len(op.SSIDCredentialHistory) != 0 || (op.SourceUID == "") != (op.SourceVersion == "") {
		return false
	}
	count := 0
	for _, d := range e.desired {
		if d.resource() == op.Resource {
			if d.name == "" || credentialSlot(d.kind) == "" {
				return false
			}
			count++
		}
	}
	return count == 1
}

func (e *siteCredentialExecutor) observeKnownTask(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	if e == nil || e.client == nil || !siteCredentialTaskScope(e, op, now) {
		return false, errConfigurationTransition
	}
	before, err := e.observe(ctx)
	if err != nil {
		return false, err
	}
	matched, converged := false, false
	for _, o := range before {
		if o.desired.resource() == op.Resource {
			if o.siteID != op.TargetID || o.credentialID != op.ParentID || o.pending {
				return false, errConfigurationStale
			}
			matched, converged = true, o.matches
		}
	}
	if !matched {
		return false, errConfigurationStale
	}
	done, err := e.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil {
		return false, err
	}
	after, err := e.observe(ctx)
	if err != nil {
		return false, err
	}
	if !reflect.DeepEqual(before, after) {
		return false, errConfigurationStale
	}
	return done && converged, nil
}

func assignmentExecutor(executor configurationExecutor) *siteCredentialExecutor {
	switch e := executor.(type) {
	case *networkSectionExecutor:
		return e.assignments
	case *networkCredentialConfigurationExecutor:
		if e.network != nil {
			return e.network.assignments
		}
	}
	return nil
}

// Compose observation-only recovery with an existing plan rather than replacing
// other resource observers or creating a second assignment execution path.
func bindSiteCredentialTaskRecovery(plan *configurationApplyPlan) {
	e := assignmentExecutor(plan.executor)
	if e == nil || e.count() == 0 {
		return
	}
	previousRecovery, previousObserver := plan.recoverKnownTask, plan.observeCompletion
	plan.recoverKnownTask = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		if op != nil && op.Section == "network_settings" && strings.HasPrefix(op.Resource, "credential-assignment:") {
			return op.Phase == "Unknown" && siteCredentialTaskScope(e, op, time.Now())
		}
		return previousRecovery != nil && previousRecovery(op)
	}
	plan.observeCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
		if op != nil && op.Section == "network_settings" && strings.HasPrefix(op.Resource, "credential-assignment:") {
			return e.observeKnownTask(ctx, op, now)
		}
		if previousObserver != nil {
			return previousObserver(ctx, op, now)
		}
		return e.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	}
}
