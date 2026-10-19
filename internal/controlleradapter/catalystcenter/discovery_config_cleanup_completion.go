// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Independent discovery obligation; the existing feature finalizer is unchanged.
const discoveryConfigFinalizer = "config.cisco.vk/catalyst-center-discovery-cleanup"
const discoveryConfigCleanupCondition = "ControllerCleanupComplete"

type discoveryConfigCompletion struct {
	ControllerUID, ConfigUID, SpecDigest, DeletionDigest, EvidenceDigest string
	SourceUID, SourceVersion, ModelDigest                                string
	ControllerGeneration, ConfigGeneration                               int64
	ScopeDigest                                                          string `json:"ScopeDigest,omitempty"`
	CredentialDigest                                                     string `json:"CredentialDigest,omitempty"`
}

func discoveryConfigKnownWorkClosed(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	if nc == nil || c == nil || validateDiscoveryCleanupEvidence(nc, c, true) != nil {
		return false
	}
	state := nc.Status.Configuration
	if state == nil {
		return true
	}
	if state.Operation != nil {
		return false
	}
	hashes, ids := map[string]bool{}, map[string]bool{}
	for _, r := range state.ActionApplications {
		if r.Operation.ConfigUID != string(c.UID) {
			continue
		}
		hash := discoveryOperationNameHash(r.Operation.Resource)
		if r.Operation.Action != "Delete" || hashes[hash] || ids[r.Operation.TargetID] {
			return false
		}
		hashes[hash], ids[r.Operation.TargetID] = true, true
	}
	return true
}

func discoveryCompletionIdentity(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) discoveryConfigCompletion {
	return discoveryConfigCompletion{ControllerUID: string(nc.UID), ConfigUID: string(c.UID), ControllerGeneration: nc.Generation, ConfigGeneration: c.Generation, SpecDigest: featureIdentityDigest(c.Spec), DeletionDigest: featureIdentityDigest(c.DeletionTimestamp), EvidenceDigest: featureIdentityDigest(nc.Status.Configuration)}
}

// Complete two native absence/cohort reads around unchanged config, source and
// retained evidence. Empty/unowned configs never claim ownership or adopt names.
func (a *adapter) observeDiscoveryConfigCompletion(ctx context.Context, c *configapi.NetworkControllerConfig) (*discoveryConfigCompletion, error) {
	if c == nil {
		return nil, errConfigurationTransition
	}
	c = c.DeepCopy()
	nc, source, err := a.discoveryCleanupReadInput(ctx, c, nil, true)
	if err != nil {
		return nil, err
	}
	if !discoveryConfigKnownWorkClosed(nc, c) {
		return nil, errConfigurationBusy
	}
	baseline := nc.Status.Configuration.DeepCopy()
	e := &ownedDiscoveryExecutor{base: &discoveryExecutor{client: a.client}, owner: string(c.UID), prune: true}
	e.bindRetiredHistory(nc, string(c.UID))
	if baseline != nil {
		e.receipts = baseline.ActionApplications
	}
	for range 2 {
		actual, err := a.client.discoveries(ctx)
		if err != nil {
			return nil, err
		}
		candidates, err := e.pruneCandidates(actual)
		if err != nil {
			return nil, err
		}
		if len(candidates) != 0 {
			return nil, errConfigurationBusy
		}
		current, fresh, err := a.discoveryCleanupReadInput(ctx, c, nil, true)
		if err != nil {
			return nil, err
		}
		if current.UID != nc.UID || current.Generation != nc.Generation || current.ResourceVersion != nc.ResourceVersion || !reflect.DeepEqual(current.Status.Configuration, baseline) || fresh.uid != source.uid || fresh.version != source.version || !reflect.DeepEqual(fresh.raw, source.raw) {
			return nil, errConfigurationStale
		}
	}
	proof := discoveryCompletionIdentity(nc, c)
	proof.SourceUID, proof.SourceVersion, proof.ModelDigest = string(source.uid), source.version, discoverySourceModelDigest(source)
	proof.ScopeDigest = discoverySourceScopeDigest(source)
	return &proof, nil
}

// Pure manager-side validation: no client, credential or product API. A worker
// attests native absence; the manager independently checks immutable identity,
// complete retained evidence and the exact canonical condition message.
func discoveryDeletionCompleted(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	if c == nil {
		return false
	}
	if condition := meta.FindStatusCondition(c.Status.Conditions, featureConfigCleanupCondition); condition != nil && condition.Reason == "TagConfigFinalized" {
		return tagDeletionCompleted(nc, c)
	}
	if discoveryCredentialHistoryOwner(nc, c) || discoveryCredentialDeletionSpecShape(c) {
		return discoveryCredentialDeletionCompleted(nc, c)
	}
	if nc == nil || !discoveryConfigCleanupShape(c) || !slices.Contains(c.Finalizers, discoveryConfigFinalizer) || hasFeatureConfigFinalizer(c) || nc.Spec.Type != TypeName || nc.Spec.Paused || !nc.DeletionTimestamp.IsZero() || controlleradapter.ValidateConfigMetadataContract(nc, c) != nil || !discoveryConfigKnownWorkClosed(nc, c) {
		return false
	}
	condition := meta.FindStatusCondition(c.Status.Conditions, discoveryConfigCleanupCondition)
	if condition == nil || condition.Status != metav1.ConditionTrue || condition.ObservedGeneration != c.Generation || condition.Reason != "DiscoveryConfigFinalized" {
		return false
	}
	var proof discoveryConfigCompletion
	if json.Unmarshal([]byte(condition.Message), &proof) != nil {
		return false
	}
	expected := discoveryCompletionIdentity(nc, c)
	if c.Spec.Source.Inline != nil {
		if proof.SourceUID != "" || proof.SourceVersion != "" || !pureDiscoverySource(c.Spec.Source.Inline.Raw) || proof.ModelDigest != featureIdentityDigest(c.Spec.Source.Inline.Raw) {
			return false
		}
	} else {
		if c.Spec.Source.ConfigMapRef == nil || proof.SourceUID == "" || proof.SourceVersion == "" || len(proof.ModelDigest) != 64 {
			return false
		}
		if _, err := hex.DecodeString(proof.ModelDigest); err != nil {
			return false
		}
	}
	if proof.ScopeDigest != "" {
		scope := discoveryScopeForDeletion(nc, c)
		if scope == nil || featureIdentityDigest(scope) != proof.ScopeDigest || scope.SourceUID != proof.SourceUID || scope.SourceVersion != proof.SourceVersion || scope.ModelDigest != proof.ModelDigest {
			return false
		}
		expected.ScopeDigest = proof.ScopeDigest
	} else if discoveryScopeForDeletion(nc, c) != nil {
		return false
	}
	expected.SourceUID, expected.SourceVersion, expected.ModelDigest = proof.SourceUID, proof.SourceVersion, proof.ModelDigest
	raw, _ := json.Marshal(expected)
	return string(raw) == condition.Message
}

// Status-only publication; workers acquire no finalizer/metadata permissions.
// The proof is invalidated by any bound config/controller/evidence change.
func (a *adapter) publishDiscoveryConfigCompletion(ctx context.Context, c *configapi.NetworkControllerConfig) error {
	if c == nil || !slices.Contains(c.Finalizers, discoveryConfigFinalizer) {
		return errConfigurationTransition
	}
	proof, err := a.observeDiscoveryConfigCompletion(ctx, c)
	if err != nil {
		return err
	}
	nc, source, err := a.discoveryCleanupReadInput(ctx, c, nil, true)
	if err != nil {
		return err
	}
	if string(source.uid) != proof.SourceUID || source.version != proof.SourceVersion || discoverySourceModelDigest(source) != proof.ModelDigest {
		return errConfigurationStale
	}
	expected := discoveryCompletionIdentity(nc, c)
	expected.ScopeDigest = discoverySourceScopeDigest(source)
	expected.SourceUID, expected.SourceVersion, expected.ModelDigest = proof.SourceUID, proof.SourceVersion, proof.ModelDigest
	if !reflect.DeepEqual(expected, *proof) || !discoveryConfigKnownWorkClosed(nc, c) {
		return errConfigurationStale
	}
	return a.publishObservedDiscoveryConfigCompletion(ctx, c, nc, proof)
}

// Both worker completion families share fresh config identity, retirement
// publication and status-only completion. No worker metadata privilege is added.
func (a *adapter) publishObservedDiscoveryConfigCompletion(ctx context.Context, c *configapi.NetworkControllerConfig, nc *cvk.NetworkController, proof *discoveryConfigCompletion) error {
	var fresh configapi.NetworkControllerConfig
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), &fresh); err != nil {
		return err
	}
	if fresh.UID != c.UID || fresh.Generation != c.Generation || fresh.ResourceVersion != c.ResourceVersion || !reflect.DeepEqual(fresh.Spec, c.Spec) || !reflect.DeepEqual(fresh.DeletionTimestamp, c.DeletionTimestamp) || !slices.Contains(fresh.Finalizers, discoveryConfigFinalizer) {
		return errConfigurationStale
	}
	if err := a.retainDiscoveryRetirement(ctx, nc, c, proof); err != nil {
		return err
	}
	raw, _ := json.Marshal(proof)
	fresh.Status.ObservedGeneration = fresh.Generation
	fresh.Status.Phase = configapi.NetworkControllerConfigPhaseWaiting
	meta.SetStatusCondition(&fresh.Status.Conditions, condition(discoveryConfigCleanupCondition, true, "DiscoveryConfigFinalized", string(raw), fresh.Generation))
	meta.SetStatusCondition(&fresh.Status.Conditions, condition(configapi.NetworkControllerConfigConditionReady, false, "DiscoveryConfigFinalized", "Deleting config awaits manager finalization", fresh.Generation))
	return a.statusWriter.Status().Update(ctx, &fresh)
}
