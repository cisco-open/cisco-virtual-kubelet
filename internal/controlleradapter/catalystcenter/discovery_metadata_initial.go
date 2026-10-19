// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
)

// Routing predicates only. Source, ownership, native effects and the complete
// lineage still pass the existing compiler/dispatch/receipt gates.
func discoveryCredentialMetadataCreatorRecorded(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	if nc == nil || nc.Status.Configuration == nil || c == nil || c.UID == "" {
		return false
	}
	for _, row := range nc.Status.Configuration.ActionApplications {
		if row.Operation.ConfigUID == string(c.UID) && discoveryCredentialReceiptSelected(row.Resource) && discoveryCredentialInputScopePrefix(row.Operation.Resource) == discoveryCredentialMetadataScopePrefix && len(row.Operation.CredentialVersions) == 0 {
			return true
		}
	}
	return false
}

// Retain/default intent has no cleanup finalizer. Delete intent still requires
// manager enrollment; changing retention must not silently discard that fence.
func discoveryMetadataLifecycleShape(c *configapi.NetworkControllerConfig) bool {
	if !discoveryCredentialLifecycleShape(c) || hasFeatureConfigFinalizer(c) {
		return false
	}
	if c.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete {
		return hasDiscoveryConfigFinalizer(c)
	}
	return !hasDiscoveryConfigFinalizer(c)
}

func discoveryNoAuthInitialSpecShape(c *configapi.NetworkControllerConfig) bool {
	if c == nil || len(c.Spec.SecretRefs) != 0 || !discoveryMetadataLifecycleShape(c) {
		return false
	}
	copy := c.DeepCopy()
	copy.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	return discoveryConfigSpecShape(copy)
}

func discoveryNoAuthInitialIntents(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, intents []discoveryCredentialIntent) bool {
	if nc == nil || c == nil || c.UID == "" || !discoveryNoAuthInitialSpecShape(c) || len(intents) == 0 || meta.FindStatusCondition(nc.Status.Conditions, discoveryRetirementCondition) != nil {
		return false
	}
	if nc.Status.Configuration != nil {
		state := nc.Status.Configuration
		for _, owner := range state.Owners {
			if configurationSectionsOverlap(owner.Section, "inventory") && (owner.Section != "inventory" || owner.ConfigUID != string(c.UID) || owner.ConfigName != c.Name) {
				return false
			}
		}
		for _, row := range state.ActionApplications {
			if row.Section == "inventory" {
				return false
			}
		}
		for _, row := range state.CredentialApplications {
			if row.Section == "inventory" {
				return false
			}
		}
		if op := state.Operation; op != nil {
			if op.ConfigUID != string(c.UID) || op.ConfigName != c.Name || discoveryCredentialInputScopePrefix(op.Resource) != discoveryCredentialMetadataScopePrefix {
				return false
			}
		}
	}
	for _, want := range intents {
		if len(want.credentials) != 0 || want.metadata["snmpVersion"] != "v3" || want.metadata["snmpMode"] != "NoAuthNoPriv" {
			return false
		}
	}
	return true
}
func discoveryNoAuthInitialSource(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source *configSourceSnapshot) bool {
	if source == nil || !discoveryNoAuthInitialSpecShape(c) {
		return false
	}
	intents, err := parseDiscoveryCredentialIntents(source.raw)
	return err == nil && discoveryNoAuthInitialIntents(nc, c, intents)
}
func discoveryCredentialPlanSelected(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source *configSourceSnapshot) bool {
	return discoveryCredentialApplyShape(c) || discoveryCredentialHistoryOwner(nc, c) || discoveryNoAuthInitialSource(nc, c, source)
}
