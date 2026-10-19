// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func (c *discoveryCredentialCohort) executionLineage() *discoveryCredentialLineage {
	view := *c.current
	owners := []string{}
	for uid := range c.archived {
		owners = append(owners, uid)
	}
	slices.Sort(owners)
	for _, uid := range owners {
		view.archives = append(view.archives, c.archived[uid])
	}
	return &view
}

// Structural budget/receipt checks only. The full ledger is still used for
// row/byte capacity. This cannot authorize foreign-history native execution.
func discoveryCredentialStructuralLineage(state *cvk.NetworkControllerConfigurationStatus, owner string) (*discoveryCredentialLineage, error) {
	local, err := discoveryCredentialLineages(state, owner)
	if err == nil {
		return local, nil
	}
	cohort, err := discoveryCredentialHistoryCohort(state, owner)
	if err != nil {
		return nil, err
	}
	return cohort.executionLineage(), nil
}

// A lost whole archived cohort must not become ordinary empty history while
// its custody/retirement marker survives. Malformed or removed custody still
// fails closed whenever foreign rows or the prior-owner marker are present.
func discoveryCredentialCustodyRequired(nc *cvk.NetworkController, owner string) bool {
	if nc == nil || nc.Status.Configuration == nil {
		return false
	}
	for _, r := range nc.Status.Configuration.ActionApplications {
		if discoveryCredentialCohortAction(r) && r.Operation.ConfigUID != owner {
			return true
		}
	}
	for _, r := range nc.Status.Configuration.CredentialApplications {
		if discoveryCredentialCohortApplication(r) && r.ConfigUID != owner {
			return true
		}
	}
	cond := meta.FindStatusCondition(nc.Status.Conditions, discoveryRetirementCondition)
	if cond == nil {
		return false
	}
	if strings.Contains(cond.Message, `"CredentialCustody"`) {
		return true
	}
	var r discoveryOwnershipRetirement
	return json.Unmarshal([]byte(cond.Message), &r) == nil && r.Completion.ConfigUID != "" && r.Completion.ConfigUID != owner && strings.HasPrefix(r.Completion.CredentialDigest, "credential-discovery-")
}

func discoveryCredentialExecutionHistory(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source *configSourceSnapshot) (*discoveryCredentialLineage, error) {
	if !discoveryCredentialCustodyRequired(nc, string(c.UID)) {
		return discoveryCredentialLineages(nc.Status.Configuration, string(c.UID))
	}
	_, cohort, err := discoveryCredentialCurrentCustody(nc, c, source)
	if err != nil {
		return nil, err
	}
	if !discoveryCredentialAttestationMatchesSource(nc, c, source) {
		return nil, errConfigurationOwned
	}
	return cohort.executionLineage(), nil
}

// Retained completion uses the already validated original scope metadata; it
// never synthesizes a missing ConfigMap body or projected credential snapshot.
func discoveryCredentialCustodyForOperation(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, op *cvk.NetworkControllerConfigurationOperation) (*discoveryCredentialLineage, error) {
	if nc == nil || c == nil || op == nil || discoveryFinalizeSelected(op.Resource) {
		return nil, errConfigurationTransition
	}
	if !((op.Action == "Create" && discoveryCredentialInputSelected(op.Resource)) || (op.Action == "Delete" && discoveryCredentialRetirementSelected(op.Resource))) {
		return nil, errConfigurationTransition
	}
	scope := discoveryCredentialScopeForContext(nc, c, op, !c.DeletionTimestamp.IsZero())
	if scope == nil {
		return nil, errConfigurationOwned
	}
	_, cohort, err := discoveryCredentialScopedCustody(nc, c, scope)
	if err != nil {
		return nil, err
	}
	return cohort.executionLineage(), nil
}

func (a *adapter) discoveryCredentialOperationHistory(ctx context.Context, nc *cvk.NetworkController, op *cvk.NetworkControllerConfigurationOperation) (*discoveryCredentialLineage, error) {
	if !discoveryCredentialCustodyRequired(nc, op.ConfigUID) {
		return discoveryCredentialLineages(nc.Status.Configuration, op.ConfigUID)
	}
	var c configapi.NetworkControllerConfig
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: a.key.Namespace, Name: op.ConfigName}, &c); err != nil {
		return nil, err
	}
	if discoveryFinalizeSelected(op.Resource) && discoveryCredentialRetirementSelected(op.Resource) {
		return discoveryCredentialCleanupHistory(nc, &c, nil, op)
	}
	return discoveryCredentialCustodyForOperation(nc, &c, op)
}
