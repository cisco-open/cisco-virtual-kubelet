// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Deletion keeps the immutable custody root and consumes the current attested
// revision, never a replacement source or a new credential version. The metadata-only branch permits observation of an
// acknowledged original task; native submission separately requires source.
func discoveryCredentialCleanupHistory(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation) (*discoveryCredentialLineage, error) {
	if nc == nil || c == nil || !discoveryCredentialCleanupObservationShape(c) {
		return nil, errConfigurationOwned
	}
	if !discoveryCredentialCustodyRequired(nc, string(c.UID)) {
		return discoveryCredentialLineages(nc.Status.Configuration, string(c.UID))
	}
	cond := meta.FindStatusCondition(nc.Status.Conditions, discoveryRetirementCondition)
	if cond == nil {
		return nil, errConfigurationOwned
	}
	var record discoveryOwnershipRetirement
	if json.Unmarshal([]byte(cond.Message), &record) != nil {
		return nil, errConfigurationOwned
	}
	link := record.CredentialCustody
	if link == nil {
		// After rollover only the new complete, sealed closure is authority.
		// It cannot prepare or recover another deletion operation.
		closed, history, err := discoveryCredentialRetiredHandoffRecord(nc)
		if err != nil || closed == nil || history == nil || source != nil || op != nil || closed.ConfigName != c.Name || closed.Completion.ConfigUID != string(c.UID) || closed.Completion.ConfigGeneration != c.Generation || closed.Completion.SpecDigest != featureIdentityDigest(c.Spec) || closed.Completion.DeletionDigest != featureIdentityDigest(c.DeletionTimestamp) {
			return nil, errConfigurationOwned
		}
		return history, nil
	}
	// Use the last attested revision. Deletion can advance metadata generation
	// but cannot silently change that revision's spec/source or projections.
	scope := meta.FindStatusCondition(c.Status.Conditions, discoveryCredentialSourceCondition)
	var proof discoverySourceScope
	if scope == nil || scope.Status != metav1.ConditionTrue || scope.Reason != discoveryCredentialSourceReason || scope.LastTransitionTime.IsZero() || scope.LastTransitionTime.After(c.DeletionTimestamp.Add(time.Second)) || json.Unmarshal([]byte(scope.Message), &proof) != nil || scope.ObservedGeneration != proof.ConfigGeneration {
		return nil, errConfigurationOwned
	}
	expected := discoveryScopeIdentity(nc, c)
	expected.ConfigGeneration = proof.ConfigGeneration
	expected.SourceUID, expected.SourceVersion, expected.ModelDigest = proof.SourceUID, proof.SourceVersion, proof.ModelDigest
	raw, _ := json.Marshal(expected)
	if scope.Message != string(raw) {
		return nil, errConfigurationOwned
	}
	_, cohort, err := discoveryCredentialScopedCustody(nc, c, &proof)
	if err != nil {
		return nil, err
	}
	if source != nil && (string(source.uid) != proof.SourceUID || source.version != proof.SourceVersion || featureIdentityDigest(source.raw) != proof.ModelDigest) {
		return nil, errConfigurationStale
	}
	if op != nil {
		data, valid := discoveryCredentialRetirementParts(op)
		parts := discoveryFinalizeParts(op.Resource)
		if !valid || !data.prune || parts == nil || !discoveryFinalizeSelected(op.Resource) || parts[0] != featureIdentityDigest(c.DeletionTimestamp) || parts[1] != featureIdentityDigest(c.Spec) || op.Action != "Delete" || op.ConfigUID != string(c.UID) || op.ConfigName != c.Name || op.ConfigGeneration != c.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != proof.SourceUID || op.SourceVersion != proof.SourceVersion || data.scope != proof.ModelDigest || discoveryCredentialCleanupSourceProof(op.Resource) != discoveryCredentialCleanupSourceCommitment(op.SourceUID, op.SourceVersion, data.scope) {
			return nil, errConfigurationOwned
		}
	}
	return cohort.executionLineage(), nil
}
