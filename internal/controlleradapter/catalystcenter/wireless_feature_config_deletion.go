// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

const featureConfigFinalizer = "config.cisco.vk/catalyst-center-feature-cleanup"
const featureConfigCleanupCondition = "ControllerCleanupComplete"
const featureFinalizePrefix = "feature-finalize:v1:"

type featureDeletionSource = metadataConfigDeletionSource

func featureDeletionShape(c *configapi.NetworkControllerConfig) bool {
	if c == nil || c.Spec.Mode != configapi.NetworkControllerApplyModeApply || len(c.Spec.ManagedSections) != 1 || c.Spec.ManagedSections[0] != "feature_templates" || len(c.Spec.SecretRefs) != 0 || !c.Spec.ModelSource.Resolved || c.Spec.ModelSource.Format != nacmodel.Format || c.Spec.ModelSource.ModelVersion != nacmodel.Version {
		return false
	}
	copy := c.DeepCopy()
	copy.Spec.Mode = configapi.NetworkControllerApplyModeReport
	copy.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
	copy.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
	return supportedConfigReport(copy)
}
func hasFeatureConfigFinalizer(c *configapi.NetworkControllerConfig) bool {
	return slices.Contains(c.Finalizers, featureConfigFinalizer)
}
func featureDeletionEligible(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	return nc != nil && nc.Spec.Type == TypeName && nc.DeletionTimestamp.IsZero() && !nc.Spec.Paused && featureDeletionShape(c) && c.DeletionTimestamp.IsZero() && c.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete && controlleradapter.ValidateConfigContract(nc, c, controlleradapter.DefaultIntentSecretPath) == nil
}
func featureDeletionContext(c *configapi.NetworkControllerConfig, cleanup bool) *featureDeletionSource {
	if !featureDeletionShape(c) || c.DeletionTimestamp.IsZero() || !slices.Contains(c.Finalizers, featureConfigFinalizer) {
		return nil
	}
	return &featureDeletionSource{ConfigUID: string(c.UID), SpecDigest: featureIdentityDigest(c.Spec), DeletionToken: featureIdentityDigest(c.DeletionTimestamp), Cleanup: cleanup}
}
func featureDeletionCleanup(source *configSourceSnapshot) bool {
	return source != nil && source.deletion != nil && source.deletion.Cleanup
}
func featureDeletionSnapshotValid(c, fresh *configapi.NetworkControllerConfig, source *configSourceSnapshot) bool {
	if source == nil || source.deletion == nil || !featureDeletionShape(fresh) || fresh.DeletionTimestamp.IsZero() || !slices.Contains(fresh.Finalizers, featureConfigFinalizer) {
		return false
	}
	d := source.deletion
	if d.ConfigUID != string(c.UID) || d.SpecDigest != featureIdentityDigest(fresh.Spec) || d.DeletionToken != featureIdentityDigest(fresh.DeletionTimestamp) {
		return false
	}
	if d.Cleanup {
		return fresh.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete && string(source.raw) == featurePruneEmpty && source.key.Name == "" && source.uid == "" && source.version == ""
	}
	return true
}

// Deletion may advance CR generation without changing the accepted source.
// This permits observation/publication of the original feature operation only.
// Finalize operations additionally bind the full spec and deletion token.
func featureOperationGenerationMatches(c *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation) bool {
	if op.ConfigGeneration == c.Generation {
		return true
	}
	if source == nil || source.deletion == nil || !featureDeletionSnapshotValid(c, c, source) {
		return false
	}
	if featureFinalizeSelected(op.Resource) {
		return op.ConfigGeneration < c.Generation && featureFinalizationMutationAllowed(c, source, op.Resource, op.Action)
	}
	return !source.deletion.Cleanup && source.deletion.ObservationGeneration == op.ConfigGeneration && op.ConfigGeneration+1 == c.Generation
}

func featureFinalizeSelected(resource string) bool {
	return strings.HasPrefix(resource, featureFinalizePrefix)
}
func featureDeleteCanonical(resource string) string {
	if !featureFinalizeSelected(resource) {
		return resource
	}
	parts := strings.Split(strings.TrimPrefix(resource, featureFinalizePrefix), ":")
	if len(parts) != 5 || !validWirelessFeatureFingerprint(parts[3]) || !validWirelessFeatureFingerprint(parts[4]) {
		return ""
	}
	return featureDeletePrefix + strings.Join(parts[:3], ":")
}
func featureFinalizationMutationAllowed(c *configapi.NetworkControllerConfig, source *configSourceSnapshot, resource, action string) bool {
	if !featureDeletionCleanup(source) || c.Spec.DeletionPolicy != configapi.NetworkControllerRetentionPolicyDelete || action != "Delete" || !featureFinalizeSelected(resource) || !featureDeletionSnapshotValid(c, c, source) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(resource, featureFinalizePrefix), ":")
	return len(parts) == 5 && parts[3] == source.deletion.DeletionToken && parts[4] == source.deletion.SpecDigest
}

// Project only this config's retained evidence. Pure metadata callbacks are safe
// for the manager; they never construct a client or make a product API request.
func featureConfigEvidence(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) (*cvk.NetworkControllerConfigurationStatus, string) {
	own := &cvk.NetworkControllerConfigurationStatus{}
	if nc.Status.Configuration != nil {
		state := nc.Status.Configuration
		if state.Operation != nil && state.Operation.ConfigUID == string(c.UID) {
			own.Operation = state.Operation.DeepCopy()
		}
		for _, owner := range state.Owners {
			if owner.ConfigUID == string(c.UID) {
				own.Owners = append(own.Owners, owner)
			}
		}
		for _, row := range state.ActionApplications {
			if row.Operation.ConfigUID == string(c.UID) {
				own.ActionApplications = append(own.ActionApplications, row)
			}
		}
		for _, row := range state.CredentialApplications {
			if row.ConfigUID == string(c.UID) {
				own.CredentialApplications = append(own.CredentialApplications, row)
			}
		}
	}
	sort.Slice(own.Owners, func(i, j int) bool { return own.Owners[i].Section < own.Owners[j].Section })
	sort.Slice(own.ActionApplications, func(i, j int) bool { return own.ActionApplications[i].Resource < own.ActionApplications[j].Resource })
	return own, featureIdentityDigest(own)
}
func featureConfigKnownWorkClosed(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	own, _ := featureConfigEvidence(nc, c)
	if own.Operation != nil {
		return false
	}
	if c.Spec.DeletionPolicy != configapi.NetworkControllerRetentionPolicyDelete {
		return true
	}
	if len(own.CredentialApplications) != 0 {
		return false
	}
	for _, owner := range own.Owners {
		if owner.Section != "feature_templates" || owner.ConfigName != c.Name {
			return false
		}
	}
	for _, row := range own.ActionApplications {
		if row.Section != "feature_templates" || row.Operation.ConfigName != c.Name || !strings.HasPrefix(row.Resource, featureCreatedPrefix) && !strings.HasPrefix(row.Resource, featureAppliedPrefix) && !strings.HasPrefix(row.Resource, featureDeletedPrefix) && !featureRetiredSelected(row.Resource) {
			return false
		}
	}
	if _, err := featureRetiredCohorts(own); err != nil {
		return false
	}
	creators, err := featureCreatedRecords(own)
	if err != nil {
		return false
	}
	if _, err = featureAppliedRecords(own); err != nil {
		return false
	}
	deleted, err := featureDeletedRecords(own)
	if err != nil || len(creators) != len(deleted) {
		return false
	}
	for key := range creators {
		if _, exists := deleted[key]; !exists {
			return false
		}
	}
	return true
}

type featureConfigCompletion struct {
	ControllerUID, ConfigUID, SpecDigest, DeletionToken, EvidenceDigest, Policy string
	ControllerGeneration, ConfigGeneration                                      int64
}

func featureConfigCompletionProof(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) featureConfigCompletion {
	_, digest := featureConfigEvidence(nc, c)
	return featureConfigCompletion{ControllerUID: string(nc.UID), ConfigUID: string(c.UID), SpecDigest: featureIdentityDigest(c.Spec), DeletionToken: featureIdentityDigest(c.DeletionTimestamp), EvidenceDigest: digest, Policy: string(c.Spec.DeletionPolicy), ControllerGeneration: nc.Generation, ConfigGeneration: c.Generation}
}
func featureDeletionCompleted(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	if nc == nil || !featureDeletionShape(c) || c.DeletionTimestamp.IsZero() || !slices.Contains(c.Finalizers, featureConfigFinalizer) || nc.Spec.Paused || !nc.DeletionTimestamp.IsZero() || nc.Spec.Type != TypeName || controlleradapter.ValidateConfigContract(nc, c, controlleradapter.DefaultIntentSecretPath) != nil || !featureConfigKnownWorkClosed(nc, c) {
		return false
	}
	condition := meta.FindStatusCondition(c.Status.Conditions, featureConfigCleanupCondition)
	if condition == nil || condition.Status != metav1.ConditionTrue || condition.ObservedGeneration != c.Generation || condition.Reason != "FeatureConfigFinalized" {
		return false
	}
	expected, _ := json.Marshal(featureConfigCompletionProof(nc, c))
	return condition.Message == string(expected)
}

// Resource families share journal draining, status CAS and finalizer proof
// publication. The manager still alone owns metadata; these callbacks supply
// only native ownership/absence semantics and bound empty source construction.
func (r *configReconciler) reconcileFeatureConfigDeletion(ctx context.Context, c *configapi.NetworkControllerConfig) (ctrl.Result, error) {
	return r.reconcileMetadataConfigDeletion(ctx, c, metadataConfigCleanup{
		finalizer: featureConfigFinalizer, reasonPrefix: "FeatureConfig", shape: featureDeletionShape,
		validOperation: func(op *cvk.NetworkControllerConfigurationOperation) bool {
			_, update := featureUpdateKey(op)
			_, deletion := featureDeleteKey(op)
			return validFeatureCreateOperation(op) || update || deletion
		},
		finalizeSelected: featureFinalizeSelected,
		cleanupSource: func(c *configapi.NetworkControllerConfig) *configSourceSnapshot {
			return &configSourceSnapshot{raw: []byte(featurePruneEmpty), deletion: featureDeletionContext(c, true)}
		},
		observationContext: func(s *configSourceSnapshot) *featureDeletionSource { return s.deletion },
		closed:             featureConfigKnownWorkClosed,
		absent: func(ctx context.Context, a *adapter, nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) (bool, error) {
			own, _ := featureConfigEvidence(nc, c)
			observer := &featurePruneExecutor{base: &featureOwnedCreateExecutor{client: a.client, owner: string(c.UID), ownerName: c.Name, receipts: own.ActionApplications, bindings: map[string]featureCreateBinding{}}}
			candidates, err := observer.snapshot(ctx)
			return err == nil && len(candidates) == 0, err
		},
	})
}
