// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// A read-only prerequisite, not dispatch authority or a finalizer completion
// proof. Public discovery CR deletion remains unavailable until the lifecycle
// binds preparation, acknowledged tasks, absence receipts and manager release.
type discoveryConfigCleanupObservation struct {
	ControllerVersion                                                              string
	ConfigUID, ConfigVersion, SpecDigest, DeletionDigest, SourceUID, SourceVersion string
	ControllerUID                                                                  string
	ConfigGeneration, ControllerGeneration                                         int64
	EvidenceDigest                                                                 string
	Candidates                                                                     []discoveryPruneCandidate
}

func discoveryConfigSpecShape(c *configapi.NetworkControllerConfig) bool {
	if c == nil || c.Spec.Mode != configapi.NetworkControllerApplyModeApply || len(c.Spec.ManagedSections) != 1 || c.Spec.ManagedSections[0] != "inventory" || len(c.Spec.SecretRefs) != 0 || c.Spec.DeletionPolicy != configapi.NetworkControllerRetentionPolicyDelete || !c.Spec.ModelSource.Resolved || c.Spec.ModelSource.Format != nacmodel.Format || c.Spec.ModelSource.ModelVersion != nacmodel.Version {
		return false
	}
	copy := c.DeepCopy()
	copy.Spec.Mode = configapi.NetworkControllerApplyModeReport
	copy.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
	copy.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
	return supportedConfigReport(copy)
}

func discoveryConfigCleanupShape(c *configapi.NetworkControllerConfig) bool {
	return discoveryConfigSpecShape(c) && !c.DeletionTimestamp.IsZero() && !c.DeletionTimestamp.After(time.Now())
}

// Check deletion identity through the actual API. Ordinary mutation snapshot
// checks deliberately refuse discovery deletion; this observation cannot weaken
// them and never calls a native writer or a Kubernetes status/metadata writer.
func (a *adapter) discoveryCleanupInput(ctx context.Context, c *configapi.NetworkControllerConfig) (*cvk.NetworkController, *configSourceSnapshot, error) {
	return a.discoveryCleanupInputForOperation(ctx, c, nil)
}

func (a *adapter) discoveryCleanupInputForOperation(ctx context.Context, c *configapi.NetworkControllerConfig, expected *cvk.NetworkControllerConfigurationOperation) (*cvk.NetworkController, *configSourceSnapshot, error) {
	return a.discoveryCleanupReadInput(ctx, c, expected, false)
}

func (a *adapter) discoveryCleanupReadInput(ctx context.Context, c *configapi.NetworkControllerConfig, expected *cvk.NetworkControllerConfigurationOperation, allowUnowned bool) (*cvk.NetworkController, *configSourceSnapshot, error) {
	if a == nil || !discoveryConfigCleanupShape(c) || c.Namespace != a.key.Namespace || c.Spec.ControllerRef.Name != a.key.Name {
		return nil, nil, errConfigurationTransition
	}
	var fresh configapi.NetworkControllerConfig
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), &fresh); err != nil {
		return nil, nil, err
	}
	if fresh.UID != c.UID || fresh.Generation != c.Generation || fresh.ResourceVersion != c.ResourceVersion || !reflect.DeepEqual(fresh.Spec, c.Spec) || !reflect.DeepEqual(fresh.DeletionTimestamp, c.DeletionTimestamp) {
		return nil, nil, errConfigurationStale
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, nil, err
	}
	if nc.Spec.Type != TypeName || nc.Spec.Paused || !nc.DeletionTimestamp.IsZero() {
		return nil, nil, errConfigurationOwned
	}
	if controlleradapter.ValidateConfigContract(nc, c, a.intentSecretPath) != nil {
		return nil, nil, errConfigurationTransition
	}
	state := nc.Status.Configuration
	if state == nil && expected != nil || state != nil && !reflect.DeepEqual(state.Operation, expected) {
		return nil, nil, errConfigurationBusy
	}
	if err := validateDiscoveryCleanupEvidence(nc, c, allowUnowned); err != nil {
		return nil, nil, err
	}

	if source := retainedDiscoverySource(nc, &fresh); source != nil {
		return nc, source, nil
	}
	source, err := loadControllerSource(ctx, a.statusReader, &fresh)
	if err != nil {
		return nil, nil, err
	}
	if !pureDiscoverySource(source.raw) {
		return nil, nil, errConfigurationTransition
	}
	if _, err := parseInventoryIntent(source.raw); err != nil {
		return nil, nil, err
	}
	return nc, source, nil
}

func (a *adapter) observeDiscoveryConfigCleanup(ctx context.Context, c *configapi.NetworkControllerConfig) (*discoveryConfigCleanupObservation, error) {
	if c == nil {
		return nil, errConfigurationTransition
	}
	c = c.DeepCopy()
	nc, source, err := a.discoveryCleanupInput(ctx, c)
	if err != nil {
		return nil, err
	}
	baseline := nc.Status.Configuration.DeepCopy()
	e := &ownedDiscoveryExecutor{base: &discoveryExecutor{client: a.client}, owner: string(c.UID), receipts: baseline.ActionApplications, prune: true}
	e.bindRetiredHistory(nc, string(c.UID))
	var expected []discoveryPruneCandidate
	for i := 0; i < 2; i++ {
		actual, err := a.client.discoveries(ctx)
		if err != nil {
			return nil, err
		}
		candidates, err := e.pruneCandidates(actual)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			expected = candidates
		} else if !reflect.DeepEqual(expected, candidates) {
			return nil, errConfigurationStale
		}
		current, fresh, err := a.discoveryCleanupInput(ctx, c)
		if err != nil {
			return nil, err
		}
		if current.UID != nc.UID || current.Generation != nc.Generation || current.ResourceVersion != nc.ResourceVersion || !reflect.DeepEqual(current.Status.Configuration, baseline) || source.uid != fresh.uid || source.version != fresh.version || !reflect.DeepEqual(source.raw, fresh.raw) {
			return nil, errConfigurationStale
		}
	}
	return &discoveryConfigCleanupObservation{ConfigUID: string(c.UID), ConfigVersion: c.ResourceVersion, ConfigGeneration: c.Generation, SpecDigest: featureIdentityDigest(c.Spec), DeletionDigest: featureIdentityDigest(c.DeletionTimestamp), ControllerUID: string(nc.UID), ControllerVersion: nc.ResourceVersion, ControllerGeneration: nc.Generation, SourceUID: string(source.uid), SourceVersion: source.version, EvidenceDigest: featureIdentityDigest(baseline), Candidates: expected}, nil
}

// Shared retained-evidence checks for mutation preparation and read-only
// completion. Empty/unowned completion never claims a section or adopts a job.
func validateDiscoveryCleanupEvidence(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, allowUnowned bool) error {
	if nc.Status.Configuration == nil {
		if allowUnowned {
			return nil
		}
		return errConfigurationOwned
	}
	state := nc.Status.Configuration
	owned := false
	for _, o := range state.Owners {
		if o.ConfigUID == string(c.UID) {
			owned = true
		}
	}
	for _, r := range state.ActionApplications {
		if r.Operation.ConfigUID == string(c.UID) {
			owned = true
		}
	}
	if !allowUnowned || owned {
		if err := configurationOwnersMatch(state, c); err != nil {
			return err
		}
	}
	for _, owner := range state.Owners {
		if owner.ConfigUID == string(c.UID) && owner.Section != "inventory" {
			return errConfigurationOwned
		}
	}
	for _, r := range state.CredentialApplications {
		if r.ConfigUID == string(c.UID) {
			return errConfigurationOwned
		}
	}
	for _, r := range state.GroupCancellations {
		if r.Operation.ConfigUID == string(c.UID) {
			return errConfigurationBusy
		}
	}
	for _, r := range state.ActionApplications {
		if r.Operation.ConfigUID != string(c.UID) {
			continue
		}
		if r.Section != "inventory" || r.Operation.ConfigName != c.Name || !validOwnedDiscoveryReceipt(r) || r.Operation.ConfigGeneration > c.Generation || r.Operation.ControllerGeneration > nc.Generation {
			return errConfigurationOwned
		}
		// Refuse foreign receipt aliases of this native UUID, including another
		// logical discovery hash. Native identity alone cannot transfer ownership.
		id := strings.TrimPrefix(r.Resource, ownedDiscoveryReceiptPrefix+discoveryOperationNameHash(r.Operation.Resource)+":")
		for _, other := range state.ActionApplications {
			if other.Operation.ConfigUID == string(c.UID) || !strings.HasPrefix(other.Resource, ownedDiscoveryReceiptPrefix) {
				continue
			}
			if !validOwnedDiscoveryReceipt(other) {
				return errConfigurationOwned
			}
			foreignID := strings.TrimPrefix(other.Resource, ownedDiscoveryReceiptPrefix+discoveryOperationNameHash(other.Operation.Resource)+":")
			if foreignID == id {
				return errConfigurationOwned
			}
		}
	}
	return nil
}
