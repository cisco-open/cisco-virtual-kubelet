// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"slices"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

func cleanupFamilyConflict(c *configapi.NetworkControllerConfig) bool {
	held := 0
	for _, f := range []string{featureConfigFinalizer, discoveryConfigFinalizer, tagConfigFinalizer} {
		if slices.Contains(c.Finalizers, f) {
			held++
		}
	}
	return held > 1
}

func tagDeletionEligible(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	return nc != nil && nc.Spec.Type == TypeName && !nc.Spec.Paused && nc.DeletionTimestamp.IsZero() && tagDeletionShape(c) && tagCleanupFinalizer(c) == tagConfigFinalizer && c.DeletionTimestamp.IsZero() && c.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete && !hasFeatureConfigFinalizer(c) && !hasDiscoveryConfigFinalizer(c) && controlleradapter.ValidateConfigMetadataContract(nc, c) == nil
}

func configurationOperationGenerationMatches(c *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation) bool {
	if source != nil && source.tagDeletion != nil {
		return tagOperationGenerationMatches(c, source, op)
	}
	return featureOperationGenerationMatches(c, source, op)
}

// Existing inventory lifecycle enrollment remains compatible. Product dispatch
// selects tags only from complete source or retained tag history; an unknown or
// mixed family is never silently adopted as owned tag state.
func (r *configReconciler) tagDeletionSelected(ctx context.Context, nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	if !tagDeletionShape(c) || !hasTagCleanupFinalizer(c) {
		return false
	}
	// Retained history determines lifecycle custody before current source does.
	if nc.Status.Configuration != nil {
		state := nc.Status.Configuration
		if state.Operation != nil && state.Operation.ConfigUID == string(c.UID) && !tagOwnedSelected(state.Operation.Resource) && !tagDeleteSelected(state.Operation.Resource) {
			return false
		}
		for _, row := range state.ActionApplications {
			if row.Operation.ConfigUID == string(c.UID) && !tagOwnedSelected(row.Operation.Resource) && !tagDeleteSelected(row.Operation.Resource) {
				return false
			}
		}
		for _, row := range state.CredentialApplications {
			if row.ConfigUID == string(c.UID) {
				return false
			}
		}
	}
	if tagOwnershipHistory(nc, c) {
		return true
	}
	source, err := loadControllerSource(ctx, r.a.statusReader, c)
	return err == nil && tagPruneSource(source.raw, c.Spec.ManagedSections) && !pureDiscoverySource(source.raw)
}

func tagDeletionCompleted(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	if nc == nil || !tagDeletionShape(c) || tagDeletionContext(c, false) == nil || !hasTagCleanupFinalizer(c) || nc.Spec.Paused || !nc.DeletionTimestamp.IsZero() || nc.Spec.Type != TypeName || controlleradapter.ValidateConfigContract(nc, c, controlleradapter.DefaultIntentSecretPath) != nil || !tagConfigKnownWorkClosed(nc, c) || !tagRetirementAllowsCompletion(nc, c) {
		return false
	}
	condition := meta.FindStatusCondition(c.Status.Conditions, featureConfigCleanupCondition)
	if condition == nil || condition.Status != metav1.ConditionTrue || condition.ObservedGeneration != c.Generation || condition.Reason != "TagConfigFinalized" {
		return false
	}
	expected, _ := json.Marshal(featureConfigCompletionProof(nc, c))
	return condition.Message == string(expected)
}

func (r *configReconciler) reconcileTagConfigDeletion(ctx context.Context, c *configapi.NetworkControllerConfig) (ctrl.Result, error) {
	return r.reconcileMetadataConfigDeletion(ctx, c, metadataConfigCleanup{
		finalizer: tagCleanupFinalizer(c), reasonPrefix: "TagConfig", shape: func(c *configapi.NetworkControllerConfig) bool {
			return tagDeletionShape(c) && hasTagCleanupFinalizer(c)
		},
		validOperation: func(op *cvk.NetworkControllerConfigurationOperation) bool {
			return validTagOwnedOperation(op) || validTagDeleteOperation(op)
		},
		finalizeSelected: tagFinalizeSelected,
		cleanupSource: func(c *configapi.NetworkControllerConfig) *configSourceSnapshot {
			return &configSourceSnapshot{raw: tagDeletionEmptySource(c), tagDeletion: tagDeletionContext(c, true)}
		},
		bindObservation: func(c *configapi.NetworkControllerConfig, s *configSourceSnapshot) {
			s.tagDeletion = tagDeletionContext(c, false)
		},
		observationContext: func(s *configSourceSnapshot) *metadataConfigDeletionSource { return s.tagDeletion },
		closed:             tagConfigKnownWorkClosed,
		retire: func(ctx context.Context, a *adapter, nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) error {
			return a.retainTagOwnershipRetirement(ctx, nc, c)
		},
		absent: func(ctx context.Context, a *adapter, nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) (bool, error) {
			return a.tagOwnedConfigAbsent(ctx, nc, c)
		},
	})
}

func (a *adapter) tagOwnedConfigAbsent(ctx context.Context, nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) (bool, error) {
	own, _ := featureConfigEvidence(nc, c)
	observer := &tagOwnedExecutor{client: a.client, owner: string(c.UID), name: c.Name, receipts: own.ActionApplications, bindings: map[string]tagOwnedBinding{}}
	candidates, err := observer.pruneSnapshot(ctx)
	if err != nil || len(candidates) != 0 {
		return false, err
	}
	cohorts, err := tagLocalRetiredCohorts(own)
	if err != nil {
		return false, err
	}
	rows, err := a.client.listTags(ctx)
	if err != nil {
		return false, err
	}
	for _, cohort := range cohorts {
		for _, row := range rows {
			if row.ID == cohort.ID || featureIdentityDigest(row.Name) == cohort.Key {
				return false, errConfigurationOwned
			}
		}
	}
	if err := tagOwnerArchiveNativeAbsent(nc.Status.Configuration, rows); err != nil {
		return false, err
	}
	return true, nil
}
