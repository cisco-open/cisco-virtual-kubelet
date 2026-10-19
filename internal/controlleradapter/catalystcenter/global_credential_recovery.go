// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"slices"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Retained applications are shared by ordinary planning and observation-only
// recovery. Updates require their original owned UUID; duplicate/corrupt rows
// cannot be replaced merely because a current native name matches.
func (e *globalCredentialExecutor) priorCredential(want globalCredentialIntent) (*cvk.NetworkControllerConfigurationCredentialApplication, error) {
	var found *cvk.NetworkControllerConfigurationCredentialApplication
	for i := range e.receipts {
		r := &e.receipts[i]
		if r.Section != "network_settings" || r.Resource != want.resource() {
			continue
		}
		if found != nil || r.ConfigUID != e.owner || !validAPIID(r.TargetID) || !configurationOperationID.MatchString(r.OperationID) || len(r.Versions) == 0 {
			return nil, errConfigurationOwned
		}
		for _, v := range r.Versions {
			if v.Key != "" {
				return nil, errConfigurationOwned
			}
		}
		if _, err := configurationCredentialRotation(r.Versions, r.Versions, globalCredentialVersionIdentity); err != nil {
			return nil, err
		}
		found = r
	}
	return found, nil
}

// Select only declared versioned definitions and the exact projected source
// identities/counters captured by this compilation. This grants observation of
// the retained task, never a submission permit. A same-owner metadata origin
// can prove a promotion Update, but cannot authorize a Create or a different UUID.
func globalCredentialTaskEnvelope(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil || !isGlobalCredentialResource(op.Section, op.Resource) || (op.Phase != "Accepted" && op.Phase != "Unknown" && op.Phase != "Verifying") || op.ConfigUID == "" || op.ConfigName == "" || op.ConfigGeneration < 1 || op.ControllerGeneration < 1 || !configurationOperationID.MatchString(op.ID) || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.ParentID != "" || op.TargetSerial != "" || op.CreatedResourceID != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CancellationRequestedAt != nil || op.PreparedAt != nil || len(op.SSIDCredentialHistory) != 0 || len(op.CredentialVersions) == 0 {
		return false
	}
	if op.Action != "Create" && op.Action != "Update" || op.Action == "Create" && op.TargetID != "" || op.Action == "Update" && !validAPIID(op.TargetID) {
		return false
	}
	return (op.SourceUID == "") == (op.SourceVersion == "")
}

func (e *globalCredentialExecutor) knownCredentialTask(op *cvk.NetworkControllerConfigurationOperation) bool {
	if e == nil || !globalCredentialTaskEnvelope(op) || e.owner == "" || e.configName == "" || op.ConfigUID != e.owner || op.ConfigName != e.configName {
		return false
	}
	matched := false
	for _, want := range e.desired {
		if want.resource() != op.Resource {
			continue
		}
		if matched || want.metadataOnly() {
			return false
		}
		prior, err := e.priorCredential(want)
		if err != nil {
			return false
		}
		root, rootID, err := e.credentialPromotionRoot(want, prior)
		if err != nil || root != nil && (op.Action != "Update" || op.TargetID != rootID || op.ID == root.Operation.ID || op.TaskID == root.Operation.TaskID || op.SubmittedAt.Before(root.VerifiedAt) || root.Operation.ConfigGeneration > op.ConfigGeneration || root.Operation.ControllerGeneration > op.ControllerGeneration) {
			return false
		}
		if op.Action == "Update" && (prior == nil && root == nil || prior != nil && prior.TargetID != op.TargetID) || prior != nil && prior.OperationID == op.ID {
			return false
		}
		if prior != nil {
			if _, err := configurationCredentialRotation(prior.Versions, op.CredentialVersions, globalCredentialVersionIdentity); err != nil {
				return false
			}
		}

		_, versions, err := want.payload(e.secrets, e.owner, op.TargetID)
		if err != nil || !slices.Equal(versions, op.CredentialVersions) {
			return false
		}
		matched = true
	}
	return matched
}

func (a *adapter) bindGlobalCredentialTaskRecovery(plan *configurationApplyPlan, e *globalCredentialExecutor) {
	plan.recoverKnownTask = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return op != nil && op.Phase == "Unknown" && (e.knownCredentialTask(op) || e.knownMetadataCredentialTask(op, time.Now()))
	}
	plan.observeCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
		if isMetadataCredentialOperation(op.Section, op.Resource) {
			if !e.knownMetadataCredentialTask(op, now) {
				return false, errConfigurationOwned
			}
			return a.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		}
		if isGlobalCredentialResource(op.Section, op.Resource) {
			if !e.knownCredentialTask(op) {
				return false, errConfigurationSecret
			}
			return a.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		}
		// Other composed network/metadata operations retain their existing observer.
		return a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	}
}
