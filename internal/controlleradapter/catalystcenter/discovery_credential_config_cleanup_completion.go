// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const discoveryCredentialCompletionPrefix = "credential-discovery-closed:v1:"

// Structural retained evidence validation, shared by budgeting and completion.
// Native execution separately checks custody/source/operation authority; manager
// release requires the complete sealed retirement record.
func discoveryCredentialCleanupEvidence(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) (*discoveryCredentialLineage, error) {
	if nc == nil || c == nil || nc.Status.Configuration == nil {
		return nil, errConfigurationOwned
	}
	if configurationOwnersMatch(nc.Status.Configuration, c) != nil || !discoveryCredentialHistoryOwner(nc, c) || discoveryOrdinaryHistoryOwner(nc, c) {
		return nil, errConfigurationOwned
	}
	for _, receipt := range nc.Status.Configuration.ActionApplications {
		if receipt.Operation.ConfigUID != string(c.UID) {
			continue
		}
		if receipt.Operation.ConfigName != c.Name || receipt.Operation.ConfigGeneration > c.Generation || receipt.Operation.ControllerGeneration > nc.Generation || !(discoveryCredentialReceiptSelected(receipt.Resource) || discoveryCredentialRetiredSelected(receipt.Resource)) {
			return nil, errConfigurationOwned
		}
	}
	for _, application := range nc.Status.Configuration.CredentialApplications {
		if application.ConfigUID == string(c.UID) && !discoveryCredentialInputSelected(application.Resource) {
			return nil, errConfigurationOwned
		}
	}
	for _, cancellation := range nc.Status.Configuration.GroupCancellations {
		if cancellation.Operation.ConfigUID == string(c.UID) {
			return nil, errConfigurationBusy
		}
	}
	return discoveryCredentialStructuralLineage(nc.Status.Configuration, string(c.UID))
}

// Keep creator/retirement AND delivered-version history when attesting closure.
// No missing/orphaned ledger can become an empty successful cleanup.
func discoveryCredentialClosedEvidence(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) (*cvk.NetworkControllerConfigurationStatus, error) {
	history, err := discoveryCredentialCleanupEvidence(nc, c)
	if err != nil {
		return nil, err
	}
	if nc.Status.Configuration.Operation != nil || len(history.latest) == 0 {
		return nil, errConfigurationBusy
	}
	for _, creator := range history.latest {
		if history.retired[creator.receipt.Operation.ID] == nil {
			return nil, errConfigurationBusy
		}
	}
	selected := &cvk.NetworkControllerConfigurationStatus{}
	for _, owner := range nc.Status.Configuration.Owners {
		if owner.ConfigUID == string(c.UID) {
			selected.Owners = append(selected.Owners, owner)
		}
	}
	for _, r := range nc.Status.Configuration.ActionApplications {
		// Mixed ordinary/credential ownership handoff is not qualified yet. Never
		// overwrite the ordinary retirement bundle with a partial typed attestation.
		if strings.HasPrefix(r.Resource, ownedDiscoveryReceiptPrefix) {
			return nil, errConfigurationOwned
		}
		if r.Operation.ConfigUID == string(c.UID) {
			selected.ActionApplications = append(selected.ActionApplications, r)
		}
	}
	for _, app := range nc.Status.Configuration.CredentialApplications {
		if app.ConfigUID == string(c.UID) {
			selected.CredentialApplications = append(selected.CredentialApplications, app)
		}
	}
	return selected, nil
}

func discoveryCredentialCompletionIdentity(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) (*discoveryConfigCompletion, error) {
	selected, err := discoveryCredentialClosedEvidence(nc, c)
	if err != nil {
		return nil, err
	}
	proof := discoveryCompletionIdentity(nc, c)
	proof.CredentialDigest = discoveryCredentialCompletionPrefix + featureIdentityDigest(selected)
	return &proof, nil
}

// Repeated complete native absence is required around unchanged config,
// controller and retained evidence. No source/projection is invented or loaded.
func (a *adapter) observeDiscoveryCredentialConfigCompletion(ctx context.Context, c *configapi.NetworkControllerConfig) (*discoveryConfigCompletion, error) {
	nc, _, err := a.discoveryCredentialCleanupBoundRead(ctx, c, nil, true)
	if err != nil {
		return nil, err
	}
	proof, err := discoveryCredentialCompletionIdentity(nc, c)
	if err != nil {
		return nil, err
	}
	history, err := discoveryCredentialCleanupHistory(nc, c, nil, nil)
	if err != nil {
		return nil, err
	}
	for range 2 {
		actual, err := a.client.discoveries(ctx)
		if err != nil {
			return nil, err
		}
		if err := history.absentRetired(actual); err != nil {
			return nil, err
		}
		current, _, err := a.discoveryCredentialCleanupBoundRead(ctx, c, nil, true)
		if err != nil {
			return nil, err
		}
		if current.ResourceVersion != nc.ResourceVersion || current.Generation != nc.Generation || !reflect.DeepEqual(current.Status.Configuration, nc.Status.Configuration) {
			return nil, errConfigurationStale
		}
	}
	return proof, nil
}

func (a *adapter) publishDiscoveryCredentialConfigCompletion(ctx context.Context, c *configapi.NetworkControllerConfig) error {
	proof, err := a.observeDiscoveryCredentialConfigCompletion(ctx, c)
	if err != nil {
		return err
	}
	nc, _, err := a.discoveryCredentialCleanupBoundRead(ctx, c, nil, true)
	if err != nil {
		return err
	}
	expected, err := discoveryCredentialCompletionIdentity(nc, c)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, proof) {
		return errConfigurationStale
	}
	return a.publishObservedDiscoveryConfigCompletion(ctx, c, nc, proof)
}

// Pure manager-side validation: no Secret bytes, source load, adapter or API.
func discoveryCredentialDeletionCompleted(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	if nc == nil || !discoveryCredentialCleanupObservationShape(c) || nc.Spec.Type != TypeName || nc.Spec.Paused || !nc.DeletionTimestamp.IsZero() || controlleradapter.ValidateConfigMetadataContract(nc, c) != nil {
		return false
	}
	if record, err := discoveryCredentialUnsubmittedRecord(nc, c); err == nil {
		cond := meta.FindStatusCondition(c.Status.Conditions, discoveryConfigCleanupCondition)
		raw, _ := json.Marshal(record.Completion)
		return cond != nil && cond.Status == metav1.ConditionTrue && cond.Reason == "DiscoveryConfigFinalized" && cond.ObservedGeneration == c.Generation && cond.Message == string(raw)
	}
	proof, err := discoveryCredentialCompletionIdentity(nc, c)
	if err != nil {
		return false
	}
	cond := meta.FindStatusCondition(c.Status.Conditions, discoveryConfigCleanupCondition)
	raw, _ := json.Marshal(proof)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "DiscoveryConfigFinalized" || cond.ObservedGeneration != c.Generation || cond.Message != string(raw) {
		return false
	}
	record, _, err := discoveryCredentialRetiredHandoffRecord(nc)
	return err == nil && record.ConfigName == c.Name && reflect.DeepEqual(record.Completion, *proof)
}
