// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Read-only recovery prerequisite. This does not authorize a status writer,
// source substitution, deletion, journal release or credential application.
// A future deletion drain must additionally bind its retained config/source
// attestation through the existing manager/worker lifecycle.
func (a *adapter) observeRetainedDiscoveryCredentialCreate(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (*discoveryCredentialCreateObservation, error) {
	if a == nil || a.client == nil || op == nil {
		return nil, errConfigurationTransition
	}
	name, valid := discoveryCredentialOperationInput(op)
	effect, scoped := discoveryCredentialRetainedEffect(op.Resource)
	if !valid || !scoped || op.Phase != "Verifying" || !configurationOperationID.MatchString(op.ID) || op.ConfigUID == "" || op.ConfigName == "" || op.ConfigGeneration < 1 || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) || !validAPIID(op.TaskID) {
		return nil, errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.Generation != op.ControllerGeneration || nc.Status.Configuration == nil || !reflect.DeepEqual(nc.Status.Configuration.Operation, op) {
		return nil, errConfigurationStale
	}
	owners := 0
	for _, owner := range nc.Status.Configuration.Owners {
		if configurationSectionsOverlap(owner.Section, "inventory") {
			if owner.Section != "inventory" || owner.ConfigUID != op.ConfigUID || owner.ConfigName != op.ConfigName {
				return nil, errConfigurationOwned
			}
			owners++
		}
	}
	if owners != 1 {
		return nil, errConfigurationOwned
	}
	if err := discoveryCredentialCreateCapacity(nc.Status.Configuration, op); err != nil {
		return nil, err
	}
	history, err := a.discoveryCredentialOperationHistory(ctx, nc, op)
	if err != nil {
		return nil, err
	}
	id, done, err := a.client.completedDiscoveryTaskResource(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
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
		found := false
		for nativeName, job := range actual {
			if discoveryPublicDigest(nativeName) != name && job.ID != id {
				continue
			}
			if found || discoveryPublicDigest(nativeName) != name || job.ID != id {
				return nil, errConfigurationOwned
			}
			observed, err := nativeDiscoveryCredentialEffect(job, effect.mask)
			if err != nil || observed != effect {
				return nil, errConfigurationOwned
			}
			if job.Condition != "Complete" || job.Status != "Inactive" {
				return nil, errConfigurationBusy
			}
			found = true
		}
		if !found {
			return nil, errConfigurationBusy
		}
		fresh, err := a.currentController(ctx)
		if err != nil {
			return nil, err
		}
		if fresh.ResourceVersion != nc.ResourceVersion || fresh.Generation != nc.Generation || !reflect.DeepEqual(fresh.Status.Configuration, nc.Status.Configuration) {
			return nil, errConfigurationStale
		}
	}
	return &discoveryCredentialCreateObservation{id: id, input: op.Resource, operationID: op.ID, controllerVersion: nc.ResourceVersion, effect: effect}, nil
}
