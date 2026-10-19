// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// A sealed cancellation is a completed worker attestation, never a creator or
// delivery. This reader requires no vanished config/source and grants no native
// permit. Legacy unsealed cancellations stay outside ownership handoff.
func discoveryCredentialSealedUnsubmittedRecord(nc *cvk.NetworkController) (*discoveryOwnershipRetirement, *discoveryCredentialLineage, error) {
	if nc == nil || nc.Spec.Type != TypeName || nc.Spec.Paused || !nc.DeletionTimestamp.IsZero() || nc.UID == "" || nc.Generation < 1 || nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil {
		return nil, nil, errConfigurationOwned
	}
	cond := meta.FindStatusCondition(nc.Status.Conditions, discoveryRetirementCondition)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "DiscoveryConfigClosed" || cond.ObservedGeneration != nc.Generation {
		return nil, nil, errConfigurationOwned
	}
	var record discoveryOwnershipRetirement
	if json.Unmarshal([]byte(cond.Message), &record) != nil {
		return nil, nil, errConfigurationOwned
	}
	raw, _ := json.Marshal(record)
	p, op := record.Completion, record.UnsubmittedOperation
	if string(raw) != cond.Message || record.CredentialCustody != nil || op == nil || record.ConfigName == "" || p.ConfigUID == "" || p.ControllerUID != string(nc.UID) || p.ControllerGeneration != nc.Generation || p.ConfigGeneration < 1 || (p.SourceUID == "") != (p.SourceVersion == "") || op.ConfigUID != p.ConfigUID || op.ConfigName != record.ConfigName || op.ConfigGeneration < 1 || (p.ConfigGeneration != op.ConfigGeneration && p.ConfigGeneration != op.ConfigGeneration+1) || op.ControllerGeneration != nc.Generation || op.SourceUID != p.SourceUID || op.SourceVersion != p.SourceVersion || op.Phase != "Prepared" || op.TaskID != "" || op.SubmittedAt != nil || !configurationOperationID.MatchString(op.ID) || discoveryCredentialInputScopePrefix(op.Resource) == "" {
		return nil, nil, errConfigurationOwned
	}
	if _, valid := discoveryCredentialOperationInput(op); !valid {
		return nil, nil, errConfigurationOwned
	}
	if _, ancestry, valid := discoveryCredentialInputParts(op.Resource); !valid || ancestry != "" {
		return nil, nil, errConfigurationOwned
	}
	for _, digest := range []string{p.SpecDigest, p.DeletionDigest, p.EvidenceDigest, p.ModelDigest, p.ScopeDigest, record.RetainedDigest, record.RetiredDigest} {
		if !lowerDiscoveryDigest(digest) {
			return nil, nil, errConfigurationOwned
		}
	}
	scope := discoverySourceScope{ControllerUID: p.ControllerUID, ConfigUID: p.ConfigUID, ControllerGeneration: p.ControllerGeneration, ConfigGeneration: op.ConfigGeneration, SpecDigest: p.SpecDigest, SourceUID: p.SourceUID, SourceVersion: p.SourceVersion, ModelDigest: p.ModelDigest}
	if featureIdentityDigest(&scope) != p.ScopeDigest || p.EvidenceDigest != featureIdentityDigest(nc.Status.Configuration) {
		return nil, nil, errConfigurationOwned
	}
	selected, cohort, err := discoveryCredentialUnsubmittedEvidence(nc.Status.Configuration, op)
	if err != nil || record.CredentialHistoryDigest != discoveryCredentialHistorySealPrefix+cohort.allDigest || record.RetainedDigest != featureIdentityDigest(selected) || p.CredentialDigest != discoveryCredentialUnsubmittedPrefix+record.RetainedDigest {
		return nil, nil, errConfigurationOwned
	}
	retired, err := discoveryRetiredRows(nc, "")
	if err != nil || record.RetiredDigest != featureIdentityDigest(retired) {
		return nil, nil, errConfigurationOwned
	}
	return &record, cohort.executionLineage(), nil
}

// Both live-source cancellation and source-free closure readback use the same
// structural witness/cohort validator. This is not execution authority.
func discoveryCredentialUnsubmittedEvidence(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) (*cvk.NetworkControllerConfigurationStatus, *discoveryCredentialCohort, error) {
	if state == nil || op == nil {
		return nil, nil, errConfigurationOwned
	}
	if state.Operation != nil && !reflect.DeepEqual(state.Operation, op) {
		return nil, nil, errConfigurationBusy
	}
	cohort, err := discoveryCredentialHistoryCohort(state, op.ConfigUID)
	if err != nil || len(cohort.current.creators) != 0 {
		return nil, nil, errConfigurationOwned
	}
	selected := &cvk.NetworkControllerConfigurationStatus{Operation: op.DeepCopy()}
	for _, owner := range state.Owners {
		if owner.ConfigUID == op.ConfigUID {
			if owner.Section != "inventory" || owner.ConfigName != op.ConfigName {
				return nil, nil, errConfigurationOwned
			}
			selected.Owners = append(selected.Owners, owner)
		}
	}
	if len(selected.Owners) != 1 {
		return nil, nil, errConfigurationOwned
	}
	for _, row := range state.ActionApplications {
		if row.Operation.ConfigUID == op.ConfigUID || row.Operation.ID == op.ID || strings.HasPrefix(row.Resource, ownedDiscoveryReceiptPrefix) {
			return nil, nil, errConfigurationOwned
		}
	}
	for _, row := range state.CredentialApplications {
		if row.ConfigUID == op.ConfigUID || row.OperationID == op.ID {
			return nil, nil, errConfigurationOwned
		}
	}
	for _, row := range state.GroupCancellations {
		if row.Operation.ConfigUID == op.ConfigUID || row.Operation.ID == op.ID {
			return nil, nil, errConfigurationOwned
		}
	}
	return selected, cohort, nil
}
