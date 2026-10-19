// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (e *discoveryCredentialExecutor) retirePlan(index int, observed *cvk.NetworkController, creator *discoveryCredentialCreatorProof, requests []*discoveryCredentialRequest, inputs []string) (*configurationWrite, error) {
	if creator == nil || creator.identity.legacy {
		return nil, errConfigurationOwned
	}
	resource := ""
	identity := configurationResourceIdentity{target: creator.identity.id}
	if index < 0 {
		resource = discoveryCredentialPrunePrefix + creator.receipt.Operation.ID + ":" + creator.digest() + ":" + featureIdentityDigest(e.source.raw)
	} else {
		base, _, ok := discoveryCredentialInputParts(inputs[index])
		if !ok {
			return nil, errConfigurationTransition
		}
		if discoveryCredentialInputScopePrefix(inputs[index]) == discoveryCredentialMetadataScopePrefix {
			base = discoveryCredentialInputPrototype(inputs[index])
		}
		resource = discoveryCredentialRetirePrefix + creator.receipt.Operation.ID + ":" + creator.digest() + ":" + base
		identity.credentials = slices.Clone(requests[index].versions)
	}
	write := &configurationWrite{resource: resource, action: "Delete", identity: identity, submit: func(ctx context.Context) (Task, error) {
		return e.a.client.deleteDiscovery(ctx, identity.target)
	}}
	guardConfigurationPreflight(write, func(ctx context.Context) error {
		current, actual, fresh, nowInputs, err := e.observe(ctx)
		if err != nil {
			return err
		}
		if !slices.Equal(inputs, nowInputs) {
			return errConfigurationStale
		}
		selected, err := e.selectCreate(current, actual, fresh, nowInputs)
		if err != nil && !(index < 0 && errors.Is(err, errConfigurationBusy)) {
			return err
		}
		if index >= 0 && selected != index {
			return errConfigurationStale
		}
		if index < 0 {
			candidate, err := e.selectPrune(current, actual)
			if err != nil {
				return err
			}
			if candidate == nil || candidate.digest() != creator.digest() {
				return errConfigurationStale
			}
		}

		history, err := discoveryCredentialExecutionHistory(current, e.config, e.source)
		if err != nil {
			return err
		}
		latest := history.latest[creator.identity.name]
		if latest == nil || latest.digest() != creator.digest() || history.retired[latest.receipt.Operation.ID] != nil || (index >= 0 && !slices.Equal(fresh[index].versions, identity.credentials)) {
			return errConfigurationStale
		}
		if observed.UID != current.UID || observed.Generation != current.Generation {
			return errConfigurationStale
		}
		valid := func(op *cvk.NetworkControllerConfigurationOperation) bool {
			_, ok := discoveryCredentialRetirementParts(op)
			return ok
		}
		err = e.a.checkOwnedConfigurationDispatch(ctx, current, e.config, e.source, "inventory", resource, "Delete", identity, valid, func(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
			if !slices.Equal(op.CredentialVersions, identity.credentials) || !reflect.DeepEqual(state.ActionApplications, current.Status.Configuration.ActionApplications) || !reflect.DeepEqual(state.CredentialApplications, current.Status.Configuration.CredentialApplications) {
				return errConfigurationStale
			}
			return discoveryCredentialRetirementCapacity(state, op)
		})
		if err != nil {
			return err
		}
		return nil
	})
	return write, nil
}

// Validate source/ownership, the exact original task, and repeated complete
// native absence. Matching a name or an unrelated successful task cannot retire
// the predecessor. Return a fresh journal snapshot for one atomic status CAS.
func (e *discoveryCredentialExecutor) observeRetirement(ctx context.Context, index int, op *cvk.NetworkControllerConfigurationOperation) (*cvk.NetworkController, error) {
	data, ok := discoveryCredentialRetirementParts(op)
	if !ok || op.Phase != "Verifying" || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) || !validAPIID(op.TaskID) {
		return nil, errConfigurationTransition
	}
	if got, err := e.operationIndex(op); err != nil || got != index {
		return nil, errConfigurationStale
	}
	if err := e.a.checkConfigurationSnapshot(ctx, e.config, e.source); err != nil {
		return nil, err
	}
	done, err := e.a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
	if err != nil {
		return nil, err
	}
	if !done {
		return nil, errConfigurationBusy
	}
	var prior *cvk.NetworkController
	for range 2 {
		nc, actual, requests, inputs, err := e.observe(ctx)
		if err != nil {
			return nil, err
		}
		if nc.Status.Configuration == nil || !reflect.DeepEqual(nc.Status.Configuration.Operation, op) || configurationOwnersMatch(nc.Status.Configuration, e.config) != nil {
			return nil, errConfigurationStale
		}
		if prior != nil && (prior.ResourceVersion != nc.ResourceVersion || !reflect.DeepEqual(prior.Status.Configuration, nc.Status.Configuration)) {
			return nil, errConfigurationStale
		}
		if data.prune {
			if featureIdentityDigest(e.source.raw) != data.scope || e.config.Spec.PrunePolicy != "Delete" {
				return nil, errConfigurationStale
			}
			history, err := discoveryCredentialExecutionHistory(nc, e.config, e.source)
			if err != nil {
				return nil, err
			}
			creator := history.creators[data.creator]
			if creator == nil {
				return nil, errConfigurationOwned
			}
			for _, want := range e.projection.intents {
				if discoveryPublicDigest(want.base.name) == creator.identity.name {
					return nil, errConfigurationOwned
				}
			}
		} else {
			if !discoveryCredentialRetirementNextMatches(data.next, inputs[index]) || !slices.Equal(requests[index].versions, op.CredentialVersions) {
				return nil, errConfigurationStale
			}
		}
		if err := discoveryCredentialRetirementCapacity(nc.Status.Configuration, op); err != nil {
			return nil, err
		}
		for name, job := range actual {
			if job.ID == op.TargetID {
				return nil, errConfigurationBusy
			}
			history, err := discoveryCredentialExecutionHistory(nc, e.config, e.source)
			if err != nil {
				return nil, err
			}
			if discoveryPublicDigest(name) == history.creators[data.creator].identity.name {
				return nil, errConfigurationOwned
			}
		}
		prior = nc
	}
	if err := e.a.checkConfigurationSnapshot(ctx, e.config, e.source); err != nil {
		return nil, err
	}
	return prior, nil
}

func discoveryCredentialRetirementReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	data, ok := discoveryCredentialRetirementParts(op)
	if !ok || op.Phase != "Verified" {
		return false
	}
	history, err := discoveryCredentialStructuralLineage(state, op.ConfigUID)
	if err != nil {
		return false
	}
	receipt := history.retired[data.creator]
	return receipt != nil && reflect.DeepEqual(receipt.receipt.Operation, *op)
}

func (e *discoveryCredentialExecutor) recordRetirement(ctx context.Context, index int, op *cvk.NetworkControllerConfigurationOperation) error {
	if _, err := e.operationIndex(op); err != nil {
		return err
	}
	current, err := e.a.currentController(ctx)
	if err != nil {
		return err
	}
	if _, err := discoveryCredentialExecutionHistory(current, e.config, e.source); err != nil {
		return err
	}
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	if current.Status.Configuration != nil && reflect.DeepEqual(current.Status.Configuration.Operation, verified) && discoveryCredentialRetirementReceiptPresent(current.Status.Configuration, verified) {
		if err := e.a.checkConfigurationSnapshot(ctx, e.config, e.source); err != nil {
			return err
		}
		data, _ := discoveryCredentialRetirementParts(op)
		if !data.prune {
			request, err := e.projection.requestFields(current, e.config, e.source, index)
			if err != nil {
				return err
			}
			if !slices.Equal(request.versions, op.CredentialVersions) {
				return errConfigurationStale
			}
		}
		return configurationOwnersMatch(current.Status.Configuration, e.config)
	}
	nc, err := e.observeRetirement(ctx, index, op)
	if err != nil {
		return err
	}
	return e.a.publishObservedDiscoveryCredentialRetirement(ctx, nc, op)
}

// Both live-source and retained-source paths authorize their observation first,
// then use this single atomic retirement/Verified publication. No credential
// application is created by a deletion.
func (a *adapter) publishObservedDiscoveryCredentialRetirement(ctx context.Context, nc *cvk.NetworkController, op *cvk.NetworkControllerConfigurationOperation) error {
	data, ok := discoveryCredentialRetirementParts(op)
	if !ok || nc == nil || nc.Status.Configuration == nil || op.Phase != "Verifying" || !reflect.DeepEqual(nc.Status.Configuration.Operation, op) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) || !validAPIID(op.TaskID) || nc.Generation != op.ControllerGeneration {
		return errConfigurationTransition
	}
	if _, err := a.discoveryCredentialOperationHistory(ctx, nc, op); err != nil {
		return err
	}
	if err := discoveryCredentialRetirementCapacity(nc.Status.Configuration, op); err != nil {
		return err
	}
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	stamp := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	if stamp.Before(op.SubmittedAt) {
		stamp = *op.SubmittedAt.DeepCopy()
	}
	nc.Status.Configuration.Operation = verified
	nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: discoveryCredentialRetiredPrefix + data.creator + ":" + op.TargetID, Operation: *verified.DeepCopy(), VerifiedAt: &stamp})
	if !discoveryCredentialRetirementReceiptPresent(nc.Status.Configuration, verified) {
		return errConfigurationTransition
	}
	if err := a.persistConfigurationActionReceipt(ctx, nc); err != nil {
		return err
	}
	stored, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if _, err := a.discoveryCredentialOperationHistory(ctx, stored, verified); err != nil {
		return err
	}
	if !discoveryCredentialRetirementReceiptPresent(stored.Status.Configuration, verified) {
		return errConfigurationStale
	}
	return nil
}
