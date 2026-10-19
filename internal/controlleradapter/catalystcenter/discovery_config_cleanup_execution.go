// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/hex"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const discoveryFinalizePrefix = "discovery-finalize:v1:"
const discoveryCredentialFinalizePrefix = "discovery-finalize:v2:"

func discoveryFinalizeSelected(resource string) bool {
	return strings.HasPrefix(resource, "discovery-finalize:")
}

// Parse the durable deletion/spec/evidence envelope without accepting nested or
// grouped resources. Canonical validation remains with the owned executor.
func discoveryFinalizeParts(resource string) []string {
	prefix, count := discoveryFinalizePrefix, 4
	credential := strings.HasPrefix(resource, discoveryCredentialFinalizePrefix)
	if credential {
		prefix, count = discoveryCredentialFinalizePrefix, 5
	} else if !strings.HasPrefix(resource, prefix) {
		return nil
	}
	p := strings.SplitN(strings.TrimPrefix(resource, prefix), ":", count)
	if len(p) != count {
		return nil
	}
	inner := p[count-1]
	if !(strings.HasPrefix(inner, ownedDiscoveryPrefix) && strings.HasSuffix(inner, discoveryPruneSuffix) || strings.HasPrefix(inner, discoveryCredentialPrunePrefix)) || credential && !strings.HasPrefix(inner, discoveryCredentialPrunePrefix) {
		return nil
	}
	for _, digest := range p[:count-1] {
		if len(digest) != 64 || strings.ToLower(digest) != digest {
			return nil
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return nil
		}
	}
	return []string{p[0], p[1], p[2], inner}
}

func discoveryCredentialCleanupSourceCommitment(uid, version, model string) string {
	return featureIdentityDigest([]string{uid, version, model})
}

func discoveryCredentialCleanupSourceProof(resource string) string {
	if !strings.HasPrefix(resource, discoveryCredentialFinalizePrefix) || discoveryFinalizeParts(resource) == nil {
		return ""
	}
	return strings.SplitN(strings.TrimPrefix(resource, discoveryCredentialFinalizePrefix), ":", 5)[3]
}

func discoveryFinalizeCanonical(resource string) string {
	if parts := discoveryFinalizeParts(resource); parts != nil {
		return parts[3]
	}
	return ""
}

func discoveryCleanupSubmissionAllowed(c *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil || !discoveryCleanupPreparationSnapshot(c, c, source) || !discoveryFinalizeSelected(op.Resource) {
		return false
	}
	p := source.discoveryCleanup
	if p.credential {
		if _, ok := discoveryCredentialRetirementParts(op); !ok {
			return false
		}
	} else if !ownedDiscoveryOperation(op) {
		return false
	}
	return configurationOperationID.MatchString(p.dispatchID) && p.dispatchID == op.ID && p.resource() == op.Resource && p.candidate.ID == op.TargetID && op.ConfigUID == string(c.UID) && op.ConfigName == c.Name && op.ConfigGeneration == c.Generation && op.ControllerGeneration == p.observation.ControllerGeneration && op.SourceUID == string(source.uid) && op.SourceVersion == source.version
}

// Reconstruct from the persisted record, never from a remembered closure. A
// missing task cannot be recovered by discovering a similarly named job.
func (a *adapter) discoveryCleanupOperationInput(ctx context.Context, c *configapi.NetworkControllerConfig, op *cvk.NetworkControllerConfigurationOperation) (*cvk.NetworkController, *configSourceSnapshot, error) {
	if c == nil || op == nil || !configurationOperationID.MatchString(op.ID) || !ownedDiscoveryOperation(op) || !discoveryFinalizeSelected(op.Resource) {
		return nil, nil, errConfigurationTransition
	}
	parts := discoveryFinalizeParts(op.Resource)
	if parts == nil || parts[0] != featureIdentityDigest(c.DeletionTimestamp) || parts[1] != featureIdentityDigest(c.Spec) || op.ConfigUID != string(c.UID) || op.ConfigName != c.Name || op.ConfigGeneration != c.Generation {
		return nil, nil, errConfigurationStale
	}
	nc, source, err := a.discoveryCleanupInputForOperation(ctx, c, op)
	if err != nil {
		return nil, nil, err
	}
	if op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
		return nil, nil, errConfigurationStale
	}
	baseline := nc.Status.Configuration.DeepCopy()
	baseline.Operation = nil
	if op.Phase != "Verified" && featureIdentityDigest(baseline) != parts[2] {
		return nil, nil, errConfigurationOwned
	}
	if op.Phase == "Prepared" {
		if op.SubmittedAt != nil || op.TaskID != "" || op.PreparedAt != nil {
			return nil, nil, errConfigurationTransition
		}
	} else if op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) {
		return nil, nil, errConfigurationTransition
	}
	o := discoveryConfigCleanupObservation{ControllerVersion: nc.ResourceVersion, ControllerUID: string(nc.UID), ControllerGeneration: nc.Generation, ConfigUID: string(c.UID), ConfigVersion: c.ResourceVersion, ConfigGeneration: c.Generation, SpecDigest: parts[1], DeletionDigest: parts[0], EvidenceDigest: parts[2], SourceUID: string(source.uid), SourceVersion: source.version}
	source.discoveryCleanup = &discoveryCleanupPreparation{observation: o, candidate: discoveryPruneCandidate{Resource: parts[3], ID: op.TargetID}}
	return nc, source, nil
}

// Advance one private lifecycle boundary per invocation. Only exact Prepared
// work can reach the shared one-use submission CAS. All later phases observe
// the original task and retain uncertainty; they never call the native writer.
// This does not enroll a public finalizer or authorize manager owner release.
func (a *adapter) advanceDiscoveryConfigCleanup(ctx context.Context, c *configapi.NetworkControllerConfig) error {
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	nc, source, err := a.discoveryCleanupOperationInput(ctx, c, op)
	if err != nil {
		return err
	}
	e := &ownedDiscoveryExecutor{base: &discoveryExecutor{client: a.client}, owner: string(c.UID), receipts: nc.Status.Configuration.DeepCopy().ActionApplications, prune: true}
	plan := configurationApplyPlan{section: "inventory", executor: e}
	a.bindOwnedDiscoveryExecutor(nc, c, source, &plan, e, "")
	switch op.Phase {
	case "Prepared":
		actual, resolved, err := e.base.observe(ctx)
		if err != nil {
			return err
		}
		candidates, err := e.pruneCandidates(actual)
		if err != nil {
			return err
		}
		if len(candidates) == 0 || candidates[0] != source.discoveryCleanup.candidate {
			return errConfigurationOwned
		}
		write, err := e.prunePlan(candidates, resolved)
		if err != nil {
			return err
		}
		source.discoveryCleanup.dispatchID = op.ID
		e.submissionGuard = func(ctx context.Context, resource, action string, identity configurationResourceIdentity) error {
			if resource != source.discoveryCleanup.candidate.Resource || action != "Delete" || !reflect.DeepEqual(identity, configurationResourceIdentity{target: op.TargetID}) {
				return errConfigurationTransition
			}
			return a.checkOwnedConfigurationDispatch(ctx, nc, c, source, "inventory", op.Resource, action, identity, func(stored *cvk.NetworkControllerConfigurationOperation) bool {
				return discoveryCleanupSubmissionAllowed(c, source, stored) && stored.ID == op.ID
			}, func(state *cvk.NetworkControllerConfigurationStatus, stored *cvk.NetworkControllerConfigurationOperation) error {
				baseline := state.DeepCopy()
				baseline.Operation = nil
				if featureIdentityDigest(baseline) != source.discoveryCleanup.observation.EvidenceDigest {
					return errConfigurationOwned
				}
				return ownedDiscoveryCapacity(state, stored, discoveryRetiredReceiptKeys(nc, stored.ConfigUID))
			})
		}
		return a.submitPreparedConfigurationReceipt(ctx, c, source, op, "inventory", op.Resource, "Delete", func(ctx context.Context) (configurationReceipt, error) {
			task, err := write.submit(ctx)
			return configurationReceipt{task: task}, err
		}, nil, configurationResourceIdentity{target: op.TargetID})
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
		return a.advanceConfiguration(ctx, op.ID, "Verifying", "")
	case "Verifying":
		return plan.recordCompletion(ctx, op)
	case "Verified":
		if !ownedDiscoveryReceiptPresent(nc.Status.Configuration, op, discoveryRetiredReceiptKeys(nc, op.ConfigUID)) {
			return errConfigurationOwned
		}
		// Revalidate retained native absence before releasing only the journal
		// slot. Owner retention and finalizer completion remain separate.
		for range 2 {
			actual, err := a.client.discoveries(ctx)
			if err != nil {
				return err
			}
			if _, err := e.pruneCandidates(actual); err != nil {
				return err
			}
		}
		if err := a.checkConfigurationSnapshot(ctx, c, source); err != nil {
			return err
		}
		return a.finishConfiguration(ctx, op.ID)
	default:
		return errConfigurationTransition
	}
}
