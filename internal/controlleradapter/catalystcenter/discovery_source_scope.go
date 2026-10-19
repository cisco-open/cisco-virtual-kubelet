// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const discoveryScopeCondition = "DiscoveryCleanupScope"
const discoveryCleanupEmptySource = `{"catalyst_center":{"inventory":{"discovery":[]}}}`

// A non-secret, status-only attestation of a validated pure-discovery source.
// Persist it before mutation; never overwrite it while any journal is occupied.
type discoverySourceScope struct {
	ControllerUID, ConfigUID, SpecDigest, SourceUID, SourceVersion, ModelDigest string
	ControllerGeneration, ConfigGeneration                                      int64
}

func discoveryScopeIdentity(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) discoverySourceScope {
	return discoverySourceScope{ControllerUID: string(nc.UID), ControllerGeneration: nc.Generation, ConfigUID: string(c.UID), ConfigGeneration: c.Generation, SpecDigest: featureIdentityDigest(c.Spec)}
}

func (r *configReconciler) recordDiscoverySourceScope(ctx context.Context, nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, status *configapi.NetworkControllerConfigStatus) (*configSourceSnapshot, bool, error) {
	if discoveryCredentialHistoryOwner(nc, c) || !discoveryConfigSpecShape(c) || !hasDiscoveryConfigFinalizer(c) || c.Spec.Source.ConfigMapRef == nil || !c.DeletionTimestamp.IsZero() || nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		return nil, false, nil
	}
	source, err := loadControllerSource(ctx, r.a.statusReader, c)
	if err != nil {
		return nil, true, err
	}
	if tagPruneSource(source.raw, c.Spec.ManagedSections) && (tagOwnershipHistory(nc, c) || !pureDiscoverySource(source.raw)) {
		return source, false, nil
	}
	if discoveryNoAuthInitialSource(nc, c, source) {
		return source, false, nil
	}
	if !pureDiscoverySource(source.raw) {
		return source, true, errConfigurationTransition
	}
	if _, err := parseInventoryIntent(source.raw); err != nil {
		return source, true, err
	}
	if source.uid == "" || source.version == "" {
		return source, true, errConfigurationStale
	}
	proof := discoveryScopeIdentity(nc, c)
	proof.SourceUID, proof.SourceVersion, proof.ModelDigest = string(source.uid), source.version, featureIdentityDigest(source.raw)
	raw, _ := json.Marshal(proof)
	old := meta.FindStatusCondition(c.Status.Conditions, discoveryScopeCondition)
	if old != nil && old.Status == metav1.ConditionTrue && old.ObservedGeneration == c.Generation && old.Reason == "PureDiscoverySource" && old.Message == string(raw) {
		return source, false, nil
	}
	current, err := r.a.currentController(ctx)
	if err != nil {
		return source, true, err
	}
	if current.UID != nc.UID || current.Generation != nc.Generation || current.ResourceVersion != nc.ResourceVersion || current.Status.Configuration != nil && current.Status.Configuration.Operation != nil {
		return source, true, errConfigurationStale
	}
	source.discoveryScopeControllerVersion = nc.ResourceVersion
	status.Phase = configapi.NetworkControllerConfigPhaseWaiting
	meta.SetStatusCondition(&status.Conditions, condition(discoveryScopeCondition, true, "PureDiscoverySource", string(raw), c.Generation))
	return source, true, nil
}

// Bind deletion recovery to the last source qualified before mutation. Later
// ConfigMap edits/replacement/deletion cannot authorize new intent while deleting.
func discoveryScopeForDeletion(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) *discoverySourceScope {
	if nc == nil || !discoveryConfigCleanupShape(c) || !hasDiscoveryConfigFinalizer(c) || hasFeatureConfigFinalizer(c) || c.Spec.Source.ConfigMapRef == nil {
		return nil
	}
	cond := meta.FindStatusCondition(c.Status.Conditions, discoveryScopeCondition)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "PureDiscoverySource" {
		return nil
	}
	var p discoverySourceScope
	if json.Unmarshal([]byte(cond.Message), &p) != nil || p.ConfigGeneration < 1 || cond.ObservedGeneration != p.ConfigGeneration || p.ConfigGeneration != c.Generation && p.ConfigGeneration+1 != c.Generation || p.SourceUID == "" || p.SourceVersion == "" || len(p.ModelDigest) != 64 || strings.ToLower(p.ModelDigest) != p.ModelDigest {
		return nil
	}
	if _, err := hex.DecodeString(p.ModelDigest); err != nil {
		return nil
	}
	expected := discoveryScopeIdentity(nc, c)
	expected.ConfigGeneration = p.ConfigGeneration
	expected.SourceUID, expected.SourceVersion, expected.ModelDigest = p.SourceUID, p.SourceVersion, p.ModelDigest
	raw, _ := json.Marshal(expected)
	if string(raw) != cond.Message {
		return nil
	}
	return &p
}

func retainedDiscoverySource(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) *configSourceSnapshot {
	p := discoveryScopeForDeletion(nc, c)
	if p == nil {
		return nil
	}
	return &configSourceSnapshot{raw: []byte(discoveryCleanupEmptySource), key: types.NamespacedName{Namespace: c.Namespace, Name: c.Spec.Source.ConfigMapRef.Name}, uid: types.UID(p.SourceUID), version: p.SourceVersion, discoveryScope: p}
}

func discoveryRetainedSourceValid(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source *configSourceSnapshot) bool {
	if source == nil || source.discoveryScope == nil || source.deletion != nil || source.secrets != nil || string(source.raw) != discoveryCleanupEmptySource {
		return false
	}
	p := discoveryScopeForDeletion(nc, c)
	return p != nil && *p == *source.discoveryScope && source.uid == types.UID(p.SourceUID) && source.version == p.SourceVersion && source.key.Namespace == c.Namespace && source.key.Name == c.Spec.Source.ConfigMapRef.Name
}

func discoverySourceModelDigest(source *configSourceSnapshot) string {
	if source.discoveryScope != nil {
		return source.discoveryScope.ModelDigest
	}
	return featureIdentityDigest(source.raw)
}

func discoverySourceScopeDigest(source *configSourceSnapshot) string {
	if source.discoveryScope == nil {
		return ""
	}
	return featureIdentityDigest(source.discoveryScope)
}
