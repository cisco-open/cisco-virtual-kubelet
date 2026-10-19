// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

const tagConfigFinalizer = "config.cisco.vk/catalyst-center-tag-cleanup"
const tagFinalizePrefix = "tag-finalize:v1:"

func tagFinalizeSelected(resource string) bool { return strings.HasPrefix(resource, tagFinalizePrefix) }

// The normal tag delete verifier, task binding and immutable creator history
// also verify finalization. The extra suffix binds the deletion event and spec;
// it cannot be stripped at the journal's submission boundary.
func tagDeleteCanonical(resource string) string {
	if !tagFinalizeSelected(resource) {
		return resource
	}
	parts := strings.Split(strings.TrimPrefix(resource, tagFinalizePrefix), ":")
	if len(parts) != 5 || !validTemplateComparisonDigest(parts[0]) || !validTemplateComparisonDigest(parts[1]) || parts[2] != "i" || !validTemplateComparisonDigest(parts[3]) || !validTemplateComparisonDigest(parts[4]) {
		return ""
	}
	return tagDeletePrefix + strings.Join(parts[:3], ":")
}

// Metadata shape is not resource admission: the worker must additionally prove
// tag-only source and ownership before synthetic empty intent can be executed.
func tagDeletionShape(c *configapi.NetworkControllerConfig) bool {
	if c == nil || c.Spec.Mode != configapi.NetworkControllerApplyModeApply || len(c.Spec.SecretRefs) != 0 || !c.Spec.ModelSource.Resolved || c.Spec.ModelSource.Format != nacmodel.Format || c.Spec.ModelSource.ModelVersion != nacmodel.Version {
		return false
	}
	sections := c.Spec.ManagedSections
	if !combinedTagSections(sections) && !(len(sections) == 1 && (sections[0] == "inventory" || sections[0] == "templates")) {
		return false
	}
	copy := c.DeepCopy()
	copy.Spec.Mode = configapi.NetworkControllerApplyModeReport
	copy.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
	copy.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
	return supportedConfigReport(copy)
}

func tagDeletionEmptySource(c *configapi.NetworkControllerConfig) []byte {
	if !tagDeletionShape(c) {
		return nil
	}
	body := map[string]any{}
	for _, section := range c.Spec.ManagedSections {
		body[section] = map[string]any{"tags": []any{}}
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": body})
	return raw
}

// Inventory retains its existing manager-owned finalizer for compatibility;
// the worker selects its resource protocol from source/receipts. Other tag
// scopes use the tag lifecycle. No manager ever reads projected source bytes.
func tagCleanupFinalizer(c *configapi.NetworkControllerConfig) string {
	if c != nil && len(c.Spec.ManagedSections) == 1 && c.Spec.ManagedSections[0] == "inventory" {
		return discoveryConfigFinalizer
	}
	return tagConfigFinalizer
}
func hasTagCleanupFinalizer(c *configapi.NetworkControllerConfig) bool {
	if c == nil || !slices.Contains(c.Finalizers, tagCleanupFinalizer(c)) {
		return false
	}
	return !slices.Contains(c.Finalizers, featureConfigFinalizer) && !(tagCleanupFinalizer(c) != discoveryConfigFinalizer && hasDiscoveryConfigFinalizer(c)) && !(tagCleanupFinalizer(c) != tagConfigFinalizer && slices.Contains(c.Finalizers, tagConfigFinalizer))
}
func tagDeletionCleanup(source *configSourceSnapshot) bool {
	return source != nil && source.tagDeletion != nil && source.tagDeletion.Cleanup
}
func tagDeletionContext(c *configapi.NetworkControllerConfig, cleanup bool) *metadataConfigDeletionSource {
	if !tagDeletionShape(c) || c.UID == "" || c.DeletionTimestamp.IsZero() || c.DeletionTimestamp.After(time.Now()) || !hasTagCleanupFinalizer(c) || hasFeatureConfigFinalizer(c) {
		return nil
	}
	return &metadataConfigDeletionSource{ConfigUID: string(c.UID), SpecDigest: featureIdentityDigest(c.Spec), DeletionToken: featureIdentityDigest(c.DeletionTimestamp), Cleanup: cleanup}
}

func tagDeletionSnapshotValid(c, fresh *configapi.NetworkControllerConfig, source *configSourceSnapshot) bool {
	if c == nil || fresh == nil || source == nil || source.tagDeletion == nil || source.deletion != nil || source.discoveryCleanup != nil || source.discoveryDrain != nil || c.UID != fresh.UID {
		return false
	}
	expected := tagDeletionContext(fresh, source.tagDeletion.Cleanup)
	d := source.tagDeletion
	if expected == nil || d.ConfigUID != expected.ConfigUID || d.SpecDigest != expected.SpecDigest || d.DeletionToken != expected.DeletionToken || featureIdentityDigest(c.Spec) != d.SpecDigest || featureIdentityDigest(c.DeletionTimestamp) != d.DeletionToken {
		return false
	}
	if !d.Cleanup {
		return tagPruneSource(source.raw, c.Spec.ManagedSections)
	}
	return fresh.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete && string(source.raw) == string(tagDeletionEmptySource(fresh)) && source.key.Name == "" && source.key.Namespace == "" && source.uid == "" && source.version == "" && len(source.templateFiles) == 0 && len(source.floorImages) == 0 && source.templateComparison == nil && source.secrets == nil && source.discoveryScope == nil && source.discoveryScopeControllerVersion == ""
}

func tagFinalizationMutationAllowed(c *configapi.NetworkControllerConfig, source *configSourceSnapshot, resource, action string) bool {
	if action != "Delete" || !tagFinalizeSelected(resource) || tagDeleteCanonical(resource) == "" || !tagDeletionSnapshotValid(c, c, source) || !source.tagDeletion.Cleanup {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(resource, tagFinalizePrefix), ":")
	return parts[3] == source.tagDeletion.DeletionToken && parts[4] == source.tagDeletion.SpecDigest
}

// A retained accepted operation may be observed across Kubernetes' deletion
// generation increment. It never gains permission to submit or change inputs.
func tagOperationGenerationMatches(c *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation) bool {
	if c == nil || op == nil {
		return false
	}
	if op.ConfigGeneration == c.Generation {
		return true
	}
	if !tagDeletionSnapshotValid(c, c, source) {
		return false
	}
	if tagFinalizeSelected(op.Resource) {
		return op.ConfigGeneration < c.Generation && tagFinalizationMutationAllowed(c, source, op.Resource, op.Action)
	}
	return !source.tagDeletion.Cleanup && source.tagDeletion.ObservationGeneration == op.ConfigGeneration && op.ConfigGeneration+1 == c.Generation && (validTagOwnedOperation(op) || validTagDeleteOperation(op))
}

// Pure completion prerequisite. A worker must also establish native absence
// and publish a bound completion proof before a manager may release metadata.
func tagConfigKnownWorkClosed(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	if nc == nil || !tagDeletionShape(c) {
		return false
	}
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
		if owner.ConfigName != c.Name || !slices.Contains(c.Spec.ManagedSections, owner.Section) {
			return false
		}
	}
	for _, row := range own.ActionApplications {
		if row.Operation.ConfigName != c.Name || !slices.Contains(c.Spec.ManagedSections, row.Section) || row.Section != row.Operation.Section || !tagOwnedSelected(row.Operation.Resource) && !tagDeleteSelected(row.Operation.Resource) {
			return false
		}
	}
	return tagOwnerCustodyValid(nc, c) == nil && tagClosedRowSet(own)
}
func tagClosedRowSet(own *cvk.NetworkControllerConfigurationStatus) bool {
	if _, err := tagLocalRetiredCohorts(own); err != nil {
		return false
	}
	records, err := tagOwnedRecords(own)
	if err != nil {
		return false
	}
	deleted, err := tagDeletedRecords(own)
	if err != nil || len(records.created) != len(deleted) {
		return false
	}
	for key := range records.created {
		if _, ok := deleted[key]; !ok {
			return false
		}
	}
	return true
}
