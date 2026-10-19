// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Rebuild private deletion-bound work from its durable record. Current source,
// projection and complete history remain required; no retained source is invented.
func (a *adapter) discoveryCredentialCleanupOperationInput(ctx context.Context, c *configapi.NetworkControllerConfig, op *cvk.NetworkControllerConfigurationOperation) (*cvk.NetworkController, *configSourceSnapshot, error) {
	return a.discoveryCredentialCleanupBoundOperation(ctx, c, op, false)
}

func (a *adapter) discoveryCredentialCleanupAcknowledgedInput(ctx context.Context, c *configapi.NetworkControllerConfig, op *cvk.NetworkControllerConfigurationOperation) (*cvk.NetworkController, error) {
	nc, _, err := a.discoveryCredentialCleanupBoundOperation(ctx, c, op, true)
	return nc, err
}

func (a *adapter) discoveryCredentialCleanupBoundOperation(ctx context.Context, c *configapi.NetworkControllerConfig, op *cvk.NetworkControllerConfigurationOperation, acknowledged bool) (*cvk.NetworkController, *configSourceSnapshot, error) {
	data, valid := discoveryCredentialRetirementParts(op)
	if c == nil || op == nil || !valid || !data.prune || !discoveryFinalizeSelected(op.Resource) || !configurationOperationID.MatchString(op.ID) || op.ConfigUID != string(c.UID) || op.ConfigName != c.Name || op.ConfigGeneration != c.Generation {
		return nil, nil, errConfigurationTransition
	}
	parts := discoveryFinalizeParts(op.Resource)
	// Legacy v1 credential records were preparation-only. They cannot grant
	// native submission or source-lost completion without a source commitment.
	if discoveryCredentialCleanupSourceProof(op.Resource) != discoveryCredentialCleanupSourceCommitment(op.SourceUID, op.SourceVersion, data.scope) {
		return nil, nil, errConfigurationStale
	}
	if parts == nil || parts[0] != featureIdentityDigest(c.DeletionTimestamp) || parts[1] != featureIdentityDigest(c.Spec) {
		return nil, nil, errConfigurationStale
	}
	nc, source, err := a.discoveryCredentialCleanupBoundRead(ctx, c, op, acknowledged)
	if err != nil {
		return nil, nil, err
	}
	if op.ControllerGeneration != nc.Generation {
		return nil, nil, errConfigurationStale
	}
	if acknowledged {
		if c.Spec.Source.Inline != nil {
			if op.SourceUID != "" || op.SourceVersion != "" || data.scope != featureIdentityDigest(c.Spec.Source.Inline.Raw) {
				return nil, nil, errConfigurationStale
			}
		} else if c.Spec.Source.ConfigMapRef == nil || op.SourceUID == "" || op.SourceVersion == "" {
			return nil, nil, errConfigurationStale
		}
	} else if source == nil || op.SourceUID != string(source.uid) || op.SourceVersion != source.version || data.scope != featureIdentityDigest(source.raw) {
		return nil, nil, errConfigurationStale
	}
	baseline := nc.Status.Configuration.DeepCopy()
	baseline.Operation = nil
	if op.Phase == "Verified" {
		if !discoveryCredentialRetirementReceiptPresent(nc.Status.Configuration, op) {
			return nil, nil, errConfigurationOwned
		}
		out := baseline.ActionApplications[:0]
		for _, receipt := range baseline.ActionApplications {
			if receipt.Operation.ID != op.ID {
				out = append(out, receipt)
			}
		}
		baseline.ActionApplications = out
	}
	if featureIdentityDigest(baseline) != parts[2] {
		return nil, nil, errConfigurationOwned
	}
	if op.Phase == "Prepared" {
		if op.SubmittedAt != nil || op.TaskID != "" {
			return nil, nil, errConfigurationTransition
		}
	} else if op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) {
		return nil, nil, errConfigurationTransition
	}
	if acknowledged {
		return nc, nil, nil
	}
	candidate := discoveryPruneCandidate{Resource: parts[3], ID: op.TargetID}
	o := discoveryConfigCleanupObservation{ControllerVersion: nc.ResourceVersion, ControllerUID: string(nc.UID), ControllerGeneration: nc.Generation, ConfigUID: string(c.UID), ConfigVersion: c.ResourceVersion, ConfigGeneration: c.Generation, SpecDigest: parts[1], DeletionDigest: parts[0], EvidenceDigest: parts[2], SourceUID: string(source.uid), SourceVersion: source.version, Candidates: []discoveryPruneCandidate{candidate}}
	source.discoveryCleanup = &discoveryCleanupPreparation{observation: o, candidate: candidate, credential: true}
	return nc, source, nil
}

func (a *adapter) observeDiscoveryCredentialCleanupCandidate(ctx context.Context, c *configapi.NetworkControllerConfig, nc *cvk.NetworkController, source *configSourceSnapshot) error {
	history, err := discoveryCredentialCleanupHistory(nc, c, source, nc.Status.Configuration.Operation)
	if err != nil {
		return err
	}
	actual, err := a.client.discoveries(ctx)
	if err != nil {
		return err
	}
	candidates, err := discoveryCredentialPruneCandidates(history, actual, nil)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return errConfigurationOwned
	}
	first := candidates[0]
	expected := discoveryPruneCandidate{Resource: discoveryCredentialPrunePrefix + first.receipt.Operation.ID + ":" + first.digest() + ":" + featureIdentityDigest(source.raw), ID: first.identity.id}
	if expected != source.discoveryCleanup.candidate {
		return errConfigurationOwned
	}
	return nil
}

// Only exact Prepared work can enter the shared one-use CAS and no-replay native
// writer. Every later phase observes the original task and preserves uncertainty.
// This private executor does not enroll/release a finalizer or transfer owners.
func (a *adapter) advanceDiscoveryCredentialConfigCleanup(ctx context.Context, c *configapi.NetworkControllerConfig) error {
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	var source *configSourceSnapshot
	if op.Phase == "Prepared" {
		nc, source, err = a.discoveryCredentialCleanupOperationInput(ctx, c, op)
	} else {
		nc, err = a.discoveryCredentialCleanupAcknowledgedInput(ctx, c, op)
	}
	if err != nil {
		return err
	}
	switch op.Phase {
	case "Prepared":
		if err := a.observeDiscoveryCredentialCleanupCandidate(ctx, c, nc, source); err != nil {
			return err
		}
		if err := a.checkConfigurationSnapshot(ctx, c, source); err != nil {
			return err
		}
		source.discoveryCleanup.dispatchID = op.ID
		identity := configurationResourceIdentity{target: op.TargetID}
		write := &configurationWrite{resource: op.Resource, action: "Delete", identity: identity, submit: func(ctx context.Context) (Task, error) {
			return a.client.deleteDiscovery(ctx, op.TargetID)
		}}
		guardConfigurationPreflight(write, func(ctx context.Context) error {
			// Re-read all owned candidates before the final live permit check. No native
			// reads follow that check; submission uses the existing no-replay transport.
			current, err := a.currentController(ctx)
			if err != nil {
				return err
			}
			if err := a.observeDiscoveryCredentialCleanupCandidate(ctx, c, current, source); err != nil {
				return err
			}
			err = a.checkOwnedConfigurationDispatch(ctx, current, c, source, "inventory", op.Resource, "Delete", identity, func(stored *cvk.NetworkControllerConfigurationOperation) bool {
				return stored != nil && stored.ID == op.ID && discoveryCleanupSubmissionAllowed(c, source, stored)
			}, func(state *cvk.NetworkControllerConfigurationStatus, stored *cvk.NetworkControllerConfigurationOperation) error {
				baseline := state.DeepCopy()
				baseline.Operation = nil
				if featureIdentityDigest(baseline) != source.discoveryCleanup.observation.EvidenceDigest {
					return errConfigurationOwned
				}
				return discoveryCredentialRetirementCapacity(state, stored)
			})
			if err != nil {
				return err
			}
			return nil
		})
		return a.submitPreparedConfigurationReceipt(ctx, c, source, op, "inventory", op.Resource, "Delete", configurationTaskWriteReceipt("inventory", write), nil, identity)
	case "Accepted", "Unknown":
		if !validAPIID(op.TaskID) {
			return errConfigurationTransition
		}
		done, err := a.observeDiscoveryDeletionTask(ctx, c, op)
		if err != nil {
			return err
		}
		if !done {
			return errConfigurationBusy
		}
		if _, err := a.discoveryCredentialCleanupAcknowledgedInput(ctx, c, op); err != nil {
			return err
		}
		return a.advanceConfiguration(ctx, op.ID, "Verifying", "")
	case "Verifying":
		observed, err := a.observeRetainedDiscoveryCredentialRetirement(ctx, op)
		if err != nil {
			return err
		}
		current, err := a.discoveryCredentialCleanupAcknowledgedInput(ctx, c, op)
		if err != nil {
			return err
		}
		if current.ResourceVersion != observed.ResourceVersion || !reflect.DeepEqual(current.Status.Configuration, observed.Status.Configuration) {
			return errConfigurationStale
		}
		return a.publishObservedDiscoveryCredentialRetirement(ctx, current, op)
	case "Verified":
		// A durable receipt is necessary, but native absence is rechecked before
		// releasing the journal slot. Remaining owned jobs may be cleaned up later.
		history, err := discoveryCredentialCleanupHistory(nc, c, nil, op)
		if err != nil {
			return err
		}
		for range 2 {
			actual, err := a.client.discoveries(ctx)
			if err != nil {
				return err
			}
			if err := history.absentRetired(actual); err != nil {
				return err
			}
			current, err := a.discoveryCredentialCleanupAcknowledgedInput(ctx, c, op)
			if err != nil {
				return err
			}
			if current.ResourceVersion != nc.ResourceVersion || !reflect.DeepEqual(current.Status.Configuration, nc.Status.Configuration) {
				return errConfigurationStale
			}
		}
		if _, err := a.discoveryCredentialCleanupAcknowledgedInput(ctx, c, op); err != nil {
			return err
		}
		return a.finishConfiguration(ctx, op.ID)
	default:
		return errConfigurationTransition
	}
}
