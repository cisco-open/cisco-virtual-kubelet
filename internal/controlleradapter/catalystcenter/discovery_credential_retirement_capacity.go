// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Reserve the retained deletion AND the successor before permitting a destructive
// request. This is a detached worst-case budget calculation, never a persisted
// absence proof and never authority to call the native delete endpoint.
func discoveryCredentialRetirementCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	data, valid := discoveryCredentialRetirementParts(op)
	if !valid || state == nil || len(state.ActionApplications) >= 256 || (!data.prune && len(state.ActionApplications) > 254) {
		return errConfigurationTransition
	}
	history, err := discoveryCredentialStructuralLineage(state, op.ConfigUID)
	if err != nil {
		return err
	}
	creator := history.creators[data.creator]
	if creator == nil || creator.identity.legacy || history.latest[creator.identity.name] != creator || history.retired[data.creator] != nil || creator.digest() != data.pair || creator.identity.id != op.TargetID {
		return errConfigurationOwned
	}
	if !data.prune {
		if err := history.transition(creator.identity.name, op.CredentialVersions); err != nil {
			return err
		}
	}
	for _, r := range state.ActionApplications {
		if r.Operation.ID == op.ID {
			return errConfigurationOwned
		}
	}
	if discoveryCredentialRetirementNextMatches(data.next, creator.receipt.Operation.Resource) {
		return errConfigurationTransition
	}
	copy := state.DeepCopy()
	stamp := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	reserved := op.DeepCopy()
	reserved.Phase = "Verified"
	reserved.TaskID = strings.Repeat("x", 128)
	reserved.SubmittedAt = &stamp
	receipt := cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: discoveryCredentialRetiredPrefix + data.creator + ":" + op.TargetID, Operation: *reserved, VerifiedAt: &stamp}
	copy.ActionApplications = append(copy.ActionApplications, receipt)
	// Re-validate the proposed link with the same parser used at reconciliation.
	// No special trusted path exists for a producer's own candidate receipt.
	if _, err := discoveryCredentialStructuralLineage(copy, op.ConfigUID); err != nil {
		return err
	}
	if data.prune {
		raw, err := json.Marshal(copy.ActionApplications)
		if err != nil || len(raw) > 256<<10 {
			return errConfigurationTransition
		}
		return nil
	}
	context := (&discoveryCredentialRetirementProof{receipt: receipt}).digest()
	next := op.DeepCopy()
	// New successors retain the public scope as well as the ancestry link.
	// Budget its largest valid mask/digest before deleting the predecessor.
	maxScope := discoveryCredentialPublicEffect{mask: (1 << len(discoveryCredentialPublicFields)) - 1, digest: strings.Repeat("a", 64)}
	prototype := discoveryCredentialScopedInput(data.next, maxScope)
	if strings.HasPrefix(data.next, discoveryCredentialMetadataScopePrefix) {
		// v4 already retains the exact public scope that the successor must use.
		prototype = data.next
	}
	next.Resource = discoveryCredentialContinuation(prototype, context)
	next.Action = "Create"
	next.TargetID = ""
	next.TaskID = ""
	next.SubmittedAt = nil
	next.Phase = "Prepared"
	next.ID = featureIdentityDigest([]string{op.ID, "successor-capacity"})[:32]
	return discoveryCredentialCreateCapacity(copy, next)
}
