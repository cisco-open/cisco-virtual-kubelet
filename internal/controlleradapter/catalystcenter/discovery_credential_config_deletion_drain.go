// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Pre-deletion work retains its original scope, generation, task and identity.
// This bridge observes/cancels only; it never issues a new controller mutation.
func (a *adapter) checkDiscoveryCredentialDrainAuthority(ctx context.Context, c *configapi.NetworkControllerConfig, op *cvk.NetworkControllerConfigurationOperation) (*cvk.NetworkController, error) {
	var fresh configapi.NetworkControllerConfig
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), &fresh); err != nil {
		return nil, err
	}
	if fresh.UID != c.UID || fresh.Generation != c.Generation || fresh.ResourceVersion != c.ResourceVersion || !reflect.DeepEqual(fresh.Spec, c.Spec) || !reflect.DeepEqual(fresh.DeletionTimestamp, c.DeletionTimestamp) || !reflect.DeepEqual(fresh.Finalizers, c.Finalizers) {
		return nil, errConfigurationStale
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if discoveryCredentialScopeForContext(nc, &fresh, op, true) == nil || nc.Status.Configuration == nil || !reflect.DeepEqual(nc.Status.Configuration.Operation, op) || configurationOwnersMatch(nc.Status.Configuration, &fresh) != nil {
		return nil, errConfigurationOwned
	}
	if discoveryCredentialCustodyRequired(nc, string(c.UID)) {
		if _, err := a.discoveryCredentialOperationHistory(ctx, nc, op); err != nil {
			return nil, err
		}
	}
	if _, err := discoveryCredentialCleanupEvidence(nc, &fresh); err != nil {
		return nil, err
	}
	return nc, nil
}

func (a *adapter) drainDiscoveryCredentialConfigOperation(ctx context.Context, c *configapi.NetworkControllerConfig) error {
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	nc, err = a.checkDiscoveryCredentialDrainAuthority(ctx, c, op)
	if err != nil {
		return err
	}
	switch op.Phase {
	case "Prepared":
		history, err := a.discoveryCredentialOperationHistory(ctx, nc, op)
		if err != nil {
			return err
		}
		// Retain the original preparation atomically with slot release. Missing
		// history alone must never be mistaken for successful empty cleanup.
		if len(history.creators) == 0 {
			return a.publishUnsubmittedDiscoveryCredentialCompletion(ctx, c)
		}
		return a.finishConfiguration(ctx, op.ID)
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
		if _, err := a.checkDiscoveryCredentialDrainAuthority(ctx, c, op); err != nil {
			return err
		}
		return a.advanceConfiguration(ctx, op.ID, "Verifying", "")
	case "Verifying":
		if discoveryCredentialRetirementSelected(op.Resource) {
			observed, err := a.observeRetainedDiscoveryCredentialRetirement(ctx, op)
			if err != nil {
				return err
			}
			current, err := a.checkDiscoveryCredentialDrainAuthority(ctx, c, op)
			if err != nil {
				return err
			}
			if current.ResourceVersion != observed.ResourceVersion || !reflect.DeepEqual(current.Status.Configuration, observed.Status.Configuration) {
				return errConfigurationStale
			}
			return a.publishObservedDiscoveryCredentialRetirement(ctx, current, op)
		}
		observed, err := a.observeRetainedDiscoveryCredentialCreate(ctx, op)
		if err != nil {
			return err
		}
		current, err := a.checkDiscoveryCredentialDrainAuthority(ctx, c, op)
		if err != nil {
			return err
		}
		return a.publishObservedDiscoveryCredentialCreate(ctx, current, op, observed)
	case "Verified":
		valid := discoveryCredentialReceiptPairPresent(nc.Status.Configuration, op)
		if discoveryCredentialRetirementSelected(op.Resource) {
			valid = discoveryCredentialRetirementReceiptPresent(nc.Status.Configuration, op)
		}
		if !valid || !discoveryCredentialHistoryValid(nc.Status.Configuration, op.ConfigUID) {
			return errConfigurationOwned
		}
		if _, err := a.checkDiscoveryCredentialDrainAuthority(ctx, c, op); err != nil {
			return err
		}
		return a.finishConfiguration(ctx, op.ID)
	default:
		return errConfigurationTransition
	}
}
