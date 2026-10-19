// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Read-only observation of an acknowledged owned deletion. This neither binds
// a new source nor authorizes deletion, successor creation or finalizer release.
func (a *adapter) observeRetainedDiscoveryCredentialRetirement(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (*cvk.NetworkController, error) {
	data, ok := discoveryCredentialRetirementParts(op)
	if a == nil || a.client == nil || !ok || op.Phase != "Verifying" || !configurationOperationID.MatchString(op.ID) || op.ConfigUID == "" || op.ConfigName == "" || op.ConfigGeneration < 1 || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) || !validAPIID(op.TaskID) {
		return nil, errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.Generation != op.ControllerGeneration || nc.Status.Configuration == nil || !reflect.DeepEqual(nc.Status.Configuration.Operation, op) {
		return nil, errConfigurationStale
	}
	if err := discoveryCredentialRetirementCapacity(nc.Status.Configuration, op); err != nil {
		return nil, err
	}
	history, err := a.discoveryCredentialOperationHistory(ctx, nc, op)
	if err != nil {
		return nil, err
	}
	creator := history.creators[data.creator]
	if creator == nil || creator.receipt.Operation.ConfigName != op.ConfigName {
		return nil, errConfigurationOwned
	}
	done, err := a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
	if err != nil {
		return nil, err
	}
	if !done {
		return nil, errConfigurationBusy
	}
	for range 2 {
		actual, err := a.client.discoveries(ctx)
		if err != nil {
			return nil, err
		}
		if err := history.absentRetired(actual); err != nil {
			return nil, err
		}
		for name, job := range actual {
			if job.ID == op.TargetID {
				return nil, errConfigurationBusy
			}
			if discoveryPublicDigest(name) == creator.identity.name {
				return nil, errConfigurationOwned
			}
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return nil, err
		}
		if current.ResourceVersion != nc.ResourceVersion || current.Generation != nc.Generation || !reflect.DeepEqual(current.Status.Configuration, nc.Status.Configuration) {
			return nil, errConfigurationStale
		}
		if _, err := a.discoveryCredentialOperationHistory(ctx, current, op); err != nil {
			return nil, err
		}
	}
	return nc, nil
}
