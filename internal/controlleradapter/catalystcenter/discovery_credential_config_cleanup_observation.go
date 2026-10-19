// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Deletion shape shared by credential observation and the public cleanup worker.
// Manager enrollment/release and native dispatch each retain separate gates.
func discoveryCredentialCleanupObservationShape(c *configapi.NetworkControllerConfig) bool {
	if c == nil || c.DeletionTimestamp.IsZero() || c.DeletionTimestamp.After(time.Now()) || c.Spec.DeletionPolicy != configapi.NetworkControllerRetentionPolicyDelete || !hasDiscoveryConfigFinalizer(c) || hasFeatureConfigFinalizer(c) {
		return false
	}
	copy := c.DeepCopy()
	copy.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
	return discoveryCredentialLifecycleShape(copy)
}

// Read only the exact deleting config and its current resolved source. Projected
// values are validated on an observation-only copy without DeletionTimestamp;
// no executor, payload builder or synthetic source is returned to the caller.
func (a *adapter) discoveryCredentialCleanupObservationInput(ctx context.Context, c *configapi.NetworkControllerConfig) (*cvk.NetworkController, *configSourceSnapshot, error) {
	return a.discoveryCredentialCleanupReadInput(ctx, c, nil)
}

func (a *adapter) discoveryCredentialCleanupReadInput(ctx context.Context, c *configapi.NetworkControllerConfig, expected *cvk.NetworkControllerConfigurationOperation) (*cvk.NetworkController, *configSourceSnapshot, error) {
	return a.discoveryCredentialCleanupBoundRead(ctx, c, expected, false)
}

// Acknowledged deletion completion checks original metadata, not disappeared
// projected bytes. It returns no synthetic source and cannot authorize dispatch.
func (a *adapter) discoveryCredentialCleanupBoundRead(ctx context.Context, c *configapi.NetworkControllerConfig, expected *cvk.NetworkControllerConfigurationOperation, acknowledged bool) (*cvk.NetworkController, *configSourceSnapshot, error) {
	if acknowledged && expected != nil && (!validAPIID(expected.TaskID) || !(expected.Phase == "Accepted" || expected.Phase == "Unknown" || expected.Phase == "Verifying" || expected.Phase == "Verified")) {
		return nil, nil, errConfigurationTransition
	}
	if a == nil || a.client == nil || !discoveryCredentialCleanupObservationShape(c) || c.Namespace != a.key.Namespace || c.Spec.ControllerRef.Name != a.key.Name {
		return nil, nil, errConfigurationTransition
	}
	var fresh configapi.NetworkControllerConfig
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), &fresh); err != nil {
		return nil, nil, err
	}
	if fresh.UID != c.UID || fresh.Generation != c.Generation || fresh.ResourceVersion != c.ResourceVersion || !reflect.DeepEqual(fresh.Spec, c.Spec) || !reflect.DeepEqual(fresh.DeletionTimestamp, c.DeletionTimestamp) || !reflect.DeepEqual(fresh.Finalizers, c.Finalizers) {
		return nil, nil, errConfigurationStale
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, nil, err
	}
	contractErr := controlleradapter.ValidateConfigMetadataContract(nc, c)
	if !acknowledged {
		contractErr = controlleradapter.ValidateConfigContract(nc, c, a.intentSecretPath)
	}
	if !nc.DeletionTimestamp.IsZero() || contractErr != nil {
		return nil, nil, errConfigurationTransition
	}
	if nc.Status.Configuration == nil || !reflect.DeepEqual(nc.Status.Configuration.Operation, expected) {
		return nil, nil, errConfigurationBusy
	}
	if _, err := discoveryCredentialCleanupEvidence(nc, c); err != nil {
		return nil, nil, err
	}
	if _, err := discoveryCredentialCleanupHistory(nc, c, nil, expected); err != nil {
		return nil, nil, err
	}
	if acknowledged {
		if expected == nil {
			if _, err := discoveryCredentialClosedEvidence(nc, c); err != nil {
				return nil, nil, err
			}
		}
		return nc, nil, nil
	}
	source, err := loadControllerSource(ctx, a.statusReader, c)
	if err != nil {
		return nil, nil, err
	}
	observationConfig := c.DeepCopy()
	observationConfig.DeletionTimestamp = nil
	projection, err := a.prepareDiscoveryCredentialProjection(nc, observationConfig, source)
	if err != nil {
		return nil, nil, err
	}
	for _, intent := range projection.intents {
		if len(intent.credentials) == 0 && !projection.metadataLifecycle {
			return nil, nil, errConfigurationOwned
		}
	}
	if _, err := discoveryCredentialCleanupHistory(nc, c, source, expected); err != nil {
		return nil, nil, err
	}
	return nc, source, nil
}

// Inspect the complete retained cohort twice before returning one preparation
// description. Same-name/UUID conflicts, public drift, dropped history, source
// changes and journal races prevent even partial cleanup. This performs GETs
// only and returns the existing observation envelope, not a dispatch permit.
func (a *adapter) observeDiscoveryCredentialConfigCleanup(ctx context.Context, c *configapi.NetworkControllerConfig) (*discoveryConfigCleanupObservation, error) {
	if c == nil {
		return nil, errConfigurationTransition
	}
	c = c.DeepCopy()
	nc, source, err := a.discoveryCredentialCleanupObservationInput(ctx, c)
	if err != nil {
		return nil, err
	}
	baseline := nc.Status.Configuration.DeepCopy()
	history, err := discoveryCredentialCleanupHistory(nc, c, source, nil)
	if err != nil {
		return nil, err
	}
	var expected []discoveryPruneCandidate
	for pass := 0; pass < 2; pass++ {
		actual, err := a.client.discoveries(ctx)
		if err != nil {
			return nil, err
		}
		creators, err := discoveryCredentialPruneCandidates(history, actual, nil)
		if err != nil {
			return nil, err
		}
		candidates := make([]discoveryPruneCandidate, 0, len(creators))
		for _, creator := range creators {
			candidates = append(candidates, discoveryPruneCandidate{Resource: discoveryCredentialPrunePrefix + creator.receipt.Operation.ID + ":" + creator.digest() + ":" + featureIdentityDigest(source.raw), ID: creator.identity.id})
		}
		if pass == 0 {
			expected = candidates
		} else if !reflect.DeepEqual(expected, candidates) {
			return nil, errConfigurationStale
		}
		current, fresh, err := a.discoveryCredentialCleanupObservationInput(ctx, c)
		if err != nil {
			return nil, err
		}
		if current.UID != nc.UID || current.Generation != nc.Generation || current.ResourceVersion != nc.ResourceVersion || !reflect.DeepEqual(current.Status.Configuration, baseline) || fresh.uid != source.uid || fresh.version != source.version || !reflect.DeepEqual(fresh.raw, source.raw) {
			return nil, errConfigurationStale
		}
	}
	return &discoveryConfigCleanupObservation{ControllerVersion: nc.ResourceVersion, ConfigUID: string(c.UID), ConfigVersion: c.ResourceVersion, SpecDigest: featureIdentityDigest(c.Spec), DeletionDigest: featureIdentityDigest(c.DeletionTimestamp), SourceUID: string(source.uid), SourceVersion: source.version, ControllerUID: string(nc.UID), ConfigGeneration: c.Generation, ControllerGeneration: nc.Generation, EvidenceDigest: featureIdentityDigest(baseline), Candidates: expected}, nil
}
