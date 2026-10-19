// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"slices"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Source pruning is an explicit Delete policy. Inspect every removed creator
// before selecting one; a later ownership conflict must prevent partial cleanup.
func (e *discoveryCredentialExecutor) selectPrune(nc *cvk.NetworkController, actual map[string]discoveryMetadata) (*discoveryCredentialCreatorProof, error) {
	if e.config.Spec.PrunePolicy != "Delete" {
		return nil, nil
	}
	history, err := discoveryCredentialExecutionHistory(nc, e.config, e.source)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, want := range e.projection.intents {
		wanted[discoveryPublicDigest(want.base.name)] = true
	}
	candidates, err := discoveryCredentialPruneCandidates(history, actual, wanted)
	if err != nil || len(candidates) == 0 {
		return nil, err
	}
	return candidates[0], nil
}

// Shared complete-cohort preflight for source removal and config cleanup. An
// empty wanted set selects all retained creators, never unrelated native jobs.
// This pure observation grants no native submission or receipt authority.
func discoveryCredentialPruneCandidates(history *discoveryCredentialLineage, actual map[string]discoveryMetadata, wanted map[string]bool) ([]*discoveryCredentialCreatorProof, error) {
	if history == nil {
		return nil, errConfigurationOwned
	}
	if err := history.absentRetired(actual); err != nil {
		return nil, err
	}
	var candidates []*discoveryCredentialCreatorProof
	for name, creator := range history.latest {
		if wanted[name] || history.retired[creator.receipt.Operation.ID] != nil {
			continue
		}
		if creator.identity.legacy {
			return nil, errConfigurationOwned
		}
		var found *discoveryMetadata
		for nativeName, row := range actual {
			if discoveryPublicDigest(nativeName) == name || row.ID == creator.identity.id {
				if discoveryPublicDigest(nativeName) != name || row.ID != creator.identity.id || found != nil {
					return nil, errConfigurationOwned
				}
				copy := row
				found = &copy
			}
		}
		if found == nil {
			return nil, errConfigurationOwned
		}
		effect, err := nativeDiscoveryCredentialEffect(*found, creator.identity.effect.mask)
		if err != nil || effect != creator.identity.effect {
			return nil, errConfigurationOwned
		}
		candidates = append(candidates, creator)
	}
	slices.SortFunc(candidates, func(a, b *discoveryCredentialCreatorProof) int {
		return strings.Compare(a.identity.name, b.identity.name)
	})
	return candidates, nil
}

// Structural routing only, never native authority. A real versioned creator
// must remain recorded; a Prepared operation or metadata-only row cannot enable
// an unwitnessed first owner. The complete parser still validates its pair.
func discoveryCredentialVersionedCreatorRecorded(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	if nc == nil || nc.Status.Configuration == nil || c == nil || c.UID == "" {
		return false
	}
	for _, row := range nc.Status.Configuration.ActionApplications {
		if row.Operation.ConfigUID == string(c.UID) && discoveryCredentialReceiptSelected(row.Resource) && discoveryCredentialInputSelected(row.Operation.Resource) && len(row.Operation.CredentialVersions) > 0 {
			return true
		}
	}
	return false
}

func discoveryCredentialHistoryOwner(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	if nc == nil || c == nil {
		return false
	}
	// A committed typed source scope reserves this lifecycle even when a
	// cancelled first preparation leaves no creator/delivery rows.
	if scope := meta.FindStatusCondition(c.Status.Conditions, discoveryCredentialSourceCondition); scope != nil && scope.Status == metav1.ConditionTrue && scope.Reason == discoveryCredentialSourceReason {
		return true
	}
	if nc.Status.Configuration == nil {
		return false
	}
	// Partial schema loss must not turn orphaned credential evidence or an
	// acknowledged in-flight operation into ordinary empty-source convergence.
	if op := nc.Status.Configuration.Operation; op != nil && op.ConfigUID == string(c.UID) && (discoveryCredentialInputSelected(op.Resource) || discoveryCredentialRetirementSelected(op.Resource)) {
		return true
	}
	for _, app := range nc.Status.Configuration.CredentialApplications {
		if app.ConfigUID == string(c.UID) && discoveryCredentialInputSelected(app.Resource) {
			return true
		}
	}
	for _, r := range nc.Status.Configuration.ActionApplications {
		if r.Operation.ConfigUID == string(c.UID) && (discoveryCredentialReceiptSelected(r.Resource) || discoveryCredentialRetiredSelected(r.Resource) || discoveryCredentialInputSelected(r.Operation.Resource) || discoveryCredentialRetirementSelected(r.Operation.Resource)) {
			return true
		}
	}
	return false
}
func discoveryOrdinaryHistoryOwner(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	if nc == nil || nc.Status.Configuration == nil || c == nil {
		return false
	}
	// A restart before the receipt must preserve the same ordinary family.
	// This selects routing only; it cannot authorize observation or submission.
	if op := nc.Status.Configuration.Operation; op != nil && op.ConfigUID == string(c.UID) && ownedDiscoverySelected(op.Resource) {
		return true
	}
	for _, r := range nc.Status.Configuration.ActionApplications {
		if r.Operation.ConfigUID == string(c.UID) && strings.HasPrefix(r.Resource, ownedDiscoveryReceiptPrefix) {
			return true
		}
	}
	return false
}
