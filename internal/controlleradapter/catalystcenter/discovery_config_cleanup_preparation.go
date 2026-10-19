// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Preparation alone has no dispatch authority. Only a freshly reconstructed,
// exact Prepared operation can receive dispatchID after owned native preflight.
// Public lifecycle enrollment remains independent from these private helpers.
type discoveryCleanupPreparation struct {
	observation discoveryConfigCleanupObservation
	candidate   discoveryPruneCandidate
	dispatchID  string
	credential  bool // set only by the credential deletion preparer
}

func (p *discoveryCleanupPreparation) resource() string {
	if p.credential {
		probe := &cvk.NetworkControllerConfigurationOperation{Section: "inventory", Action: "Delete", Resource: p.candidate.Resource, TargetID: p.candidate.ID}
		data, ok := discoveryCredentialRetirementParts(probe)
		if !ok || !data.prune {
			return ""
		}
		proof := discoveryCredentialCleanupSourceCommitment(p.observation.SourceUID, p.observation.SourceVersion, data.scope)
		return discoveryCredentialFinalizePrefix + p.observation.DeletionDigest + ":" + p.observation.SpecDigest + ":" + p.observation.EvidenceDigest + ":" + proof + ":" + p.candidate.Resource
	}
	return "discovery-finalize:v1:" + p.observation.DeletionDigest + ":" + p.observation.SpecDigest + ":" + p.observation.EvidenceDigest + ":" + p.candidate.Resource
}

func discoveryCleanupPreparationSnapshot(c, fresh *configapi.NetworkControllerConfig, source *configSourceSnapshot) bool {
	if source == nil || source.discoveryCleanup == nil || source.deletion != nil || source.discoveryDrain != nil {
		return false
	}
	p := source.discoveryCleanup
	if p.credential {
		if !discoveryCredentialCleanupObservationShape(c) || !discoveryCredentialCleanupObservationShape(fresh) || len(p.observation.Candidates) == 0 || p.candidate != p.observation.Candidates[0] || !reflect.DeepEqual(c.Finalizers, fresh.Finalizers) {
			return false
		}
		probe := &cvk.NetworkControllerConfigurationOperation{Section: "inventory", Action: "Delete", Resource: p.candidate.Resource, TargetID: p.candidate.ID}
		data, ok := discoveryCredentialRetirementParts(probe)
		if !ok || !data.prune || data.scope != featureIdentityDigest(source.raw) {
			return false
		}
	} else if !discoveryConfigCleanupShape(c) || !discoveryConfigCleanupShape(fresh) {
		return false
	}
	o := source.discoveryCleanup.observation
	return string(c.UID) == o.ConfigUID && c.Generation == o.ConfigGeneration && c.ResourceVersion == o.ConfigVersion &&
		fresh.UID == c.UID && fresh.Generation == c.Generation && fresh.ResourceVersion == c.ResourceVersion &&
		reflect.DeepEqual(c.Spec, fresh.Spec) && reflect.DeepEqual(c.DeletionTimestamp, fresh.DeletionTimestamp) &&
		featureIdentityDigest(fresh.Spec) == o.SpecDigest && featureIdentityDigest(fresh.DeletionTimestamp) == o.DeletionDigest &&
		string(source.uid) == o.SourceUID && source.version == o.SourceVersion
}

func discoveryCleanupPreparationAllowed(c *configapi.NetworkControllerConfig, source *configSourceSnapshot, section, resource, action string, identities []configurationResourceIdentity) bool {
	if !discoveryCleanupPreparationSnapshot(c, c, source) || section != "inventory" || action != "Delete" || len(identities) != 1 {
		return false
	}
	p := source.discoveryCleanup
	if resource != p.resource() || !validAPIID(p.candidate.ID) || !(p.credential && discoveryCredentialRetirementSelected(p.candidate.Resource) || !p.credential && ownedDiscoverySelected(p.candidate.Resource)) {
		return false
	}
	return reflect.DeepEqual(identities[0], configurationResourceIdentity{target: p.candidate.ID})
}

func (a *adapter) prepareDiscoveryConfigCleanup(ctx context.Context, config *configapi.NetworkControllerConfig) (*cvk.NetworkControllerConfigurationOperation, error) {
	o, err := a.observeDiscoveryConfigCleanup(ctx, config)
	if err != nil {
		return nil, err
	}
	if len(o.Candidates) == 0 {
		// An empty observation is not a durable completion/absence proof.
		return nil, errConfigurationTransition
	}
	nc, source, err := a.discoveryCleanupInput(ctx, config)
	if err != nil {
		return nil, err
	}
	return a.prepareObservedDiscoveryConfigCleanup(ctx, config, o, nc, source, false)
}

// Both cleanup families share the same exact observation binding, status CAS
// and durable readback. Product-specific observers authorize the input first.
func (a *adapter) prepareObservedDiscoveryConfigCleanup(ctx context.Context, config *configapi.NetworkControllerConfig, o *discoveryConfigCleanupObservation, nc *cvk.NetworkController, source *configSourceSnapshot, credential bool) (*cvk.NetworkControllerConfigurationOperation, error) {
	if o == nil || nc == nil || source == nil || len(o.Candidates) == 0 {
		return nil, errConfigurationTransition
	}
	if string(nc.UID) != o.ControllerUID || nc.Generation != o.ControllerGeneration || nc.ResourceVersion != o.ControllerVersion || featureIdentityDigest(nc.Status.Configuration) != o.EvidenceDigest || string(source.uid) != o.SourceUID || source.version != o.SourceVersion {
		return nil, errConfigurationStale
	}
	source.discoveryCleanup = &discoveryCleanupPreparation{observation: *o, candidate: o.Candidates[0], credential: credential}
	p := source.discoveryCleanup
	op, err := a.prepareConfiguration(ctx, config, source, "inventory", p.resource(), "Delete", configurationResourceIdentity{target: p.candidate.ID})
	if err != nil {
		return nil, err
	}
	stored, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if stored.UID != nc.UID || stored.Status.Configuration == nil || !reflect.DeepEqual(stored.Status.Configuration.Operation, op) {
		return nil, errConfigurationStale
	}
	return op, nil
}
