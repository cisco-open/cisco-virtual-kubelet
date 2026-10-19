// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func featureReceiptsForKey(state *cvk.NetworkControllerConfigurationStatus, key string) []cvk.NetworkControllerConfigurationActionApplication {
	rows := []cvk.NetworkControllerConfigurationActionApplication{}
	if state == nil {
		return rows
	}
	for _, r := range state.ActionApplications {
		if featureRetiredSelected(r.Resource) {
			continue
		}
		current, ok := featureCreateKey(r.Operation.Resource)
		if !ok {
			current, ok = featureUpdateKey(&r.Operation)
		}
		if !ok {
			current, ok = featureDeleteKey(&r.Operation)
		}
		if ok && current == key {
			rows = append(rows, r)
		}
	}
	return rows
}
func (a *adapter) bindFeaturePrune(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, plan *configurationApplyPlan) error {
	if plan.section != "feature_templates" || config.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyDelete && !featureDeletionCleanup(source) {
		return nil
	}
	if len(config.Spec.ManagedSections) != 1 || len(config.Spec.SecretRefs) != 0 || config.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete && !hasFeatureConfigFinalizer(config) {
		return errConfigurationTransition
	}
	base, ok := plan.executor.(*featureOwnedCreateExecutor)
	if !ok {
		return errConfigurationTransition
	}
	if nc.Status.Configuration != nil {
		for _, r := range nc.Status.Configuration.CredentialApplications {
			if r.ConfigUID == base.owner {
				return errConfigurationOwned
			}
		}
		for _, owner := range nc.Status.Configuration.Owners {
			if owner.ConfigUID == base.owner && owner.Section != "feature_templates" {
				return errConfigurationOwned
			}
		}
		for _, r := range base.receipts {
			if r.Operation.ConfigUID == base.owner && !strings.HasPrefix(r.Resource, featureCreatedPrefix) && !strings.HasPrefix(r.Resource, featureAppliedPrefix) && !strings.HasPrefix(r.Resource, featureDeletedPrefix) && !featureRetiredSelected(r.Resource) {
				return errConfigurationOwned
			}
		}
	}
	e := &featurePruneExecutor{base: base}
	if featureDeletionCleanup(source) {
		e.resource = func(candidate featurePruneCandidate) string {
			return featureFinalizePrefix + candidate.Key + ":" + candidate.Fingerprint + ":" + source.deletion.DeletionToken + ":" + source.deletion.SpecDigest
		}
	}
	e.guard = func(ctx context.Context, write *configurationWrite) error {
		return a.checkOwnedConfigurationDispatch(ctx, nc, config, source, plan.section, write.resource, write.action, write.identity, func(op *cvk.NetworkControllerConfigurationOperation) bool { _, ok := featureDeleteKey(op); return ok }, func(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
			key, _ := featureDeleteKey(op)
			before := &cvk.NetworkControllerConfigurationStatus{ActionApplications: base.receipts}
			if !reflect.DeepEqual(featureReceiptsForKey(before, key), featureReceiptsForKey(state, key)) {
				return errConfigurationOwned
			}
			return featureDeleteCapacity(state, op)
		})
	}
	prior := *plan
	plan.executor = e
	plan.receiptOperation = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return op != nil && featureDeleteSelected(op.Resource) || prior.recordCompletion != nil && (prior.receiptOperation == nil || prior.receiptOperation(op))
	}
	plan.recoverKnownTask = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		if op != nil && featureDeleteSelected(op.Resource) {
			return op.Phase == "Unknown" && op.ConfigUID == base.owner && op.ConfigName == base.ownerName && op.SubmittedAt != nil && validAPIID(op.TaskID) && featureDeleteAuthority(&cvk.NetworkControllerConfigurationStatus{ActionApplications: base.receipts}, op) == nil
		}
		return prior.canObserveUnknownTask(op)
	}
	plan.recordCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) error {
		if !featureDeleteSelected(op.Resource) {
			if prior.recordCompletion == nil {
				return errConfigurationTransition
			}
			return prior.recordCompletion(ctx, op)
		}
		if featureFinalizeSelected(op.Resource) && !featureFinalizationMutationAllowed(config, source, op.Resource, op.Action) {
			return errConfigurationStale
		}
		if op.Phase != "Verifying" || op.ConfigUID != base.owner || op.ConfigName != base.ownerName || !featureOperationGenerationMatches(config, source, op) || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
			return errConfigurationStale
		}
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if current.UID != nc.UID || current.Generation != nc.Generation || current.Spec.Paused || !current.DeletionTimestamp.IsZero() || current.Status.Configuration == nil || !reflect.DeepEqual(current.Status.Configuration.Operation, op) {
			return errConfigurationStale
		}
		if err = configurationOwnersMatch(current.Status.Configuration, config); err != nil {
			return err
		}
		if err = featureDeleteCapacity(current.Status.Configuration, op); err != nil {
			return err
		}
		key, _ := featureDeleteKey(op)
		if !reflect.DeepEqual(featureReceiptsForKey(&cvk.NetworkControllerConfigurationStatus{ActionApplications: base.receipts}, key), featureReceiptsForKey(current.Status.Configuration, key)) {
			return errConfigurationOwned
		}
		done, err := e.verifyDelete(ctx, op)
		if err != nil {
			return err
		}
		if !done {
			return errConfigurationBusy
		}
		if err = a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		current.Status.Configuration.Operation.Phase = "Verified"
		now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
		if op.SubmittedAt.After(now.Time) {
			now = *op.SubmittedAt.DeepCopy()
		}
		current.Status.Configuration.ActionApplications = append(current.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: op.Section, Resource: featureDeletedPrefix + key + ":" + op.TargetID, Operation: *current.Status.Configuration.Operation.DeepCopy(), VerifiedAt: &now})
		return a.persistConfigurationActionReceipt(ctx, current)
	}
	return nil
}
