// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// No-work readback has no UUID to adopt. Its original Prepared name must remain
// absent while verifying closure or transferring the old reservation.
func discoveryCredentialUnsubmittedAbsent(record *discoveryOwnershipRetirement, actual map[string]discoveryMetadata) error {
	if record.UnsubmittedOperation == nil {
		return nil
	}
	name, valid := discoveryCredentialOperationInput(record.UnsubmittedOperation)
	if !valid {
		return errConfigurationOwned
	}
	for nativeName := range actual {
		if discoveryPublicDigest(nativeName) == name {
			return errConfigurationOwned
		}
	}
	return nil
}

// Only an already committed cancellation may finish without the original source
// or projected bytes. Repeat native absence and exact config/controller/proof
// checks; never create a source, a permit or a replacement retirement record.
func (a *adapter) publishRetainedUnsubmittedDiscoveryCredentialCompletion(ctx context.Context, c *configapi.NetworkControllerConfig) error {
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	record, err := discoveryCredentialUnsubmittedRecord(nc, c)
	if err != nil {
		return err
	}
	history, err := discoveryCredentialStructuralLineage(nc.Status.Configuration, string(c.UID))
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
		if err := discoveryCredentialUnsubmittedAbsent(record, actual); err != nil {
			return err
		}
		var fresh configapi.NetworkControllerConfig
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), &fresh); err != nil {
			return err
		}
		if fresh.UID != c.UID || fresh.Generation != c.Generation || fresh.ResourceVersion != c.ResourceVersion || !reflect.DeepEqual(fresh.Spec, c.Spec) || !reflect.DeepEqual(fresh.DeletionTimestamp, c.DeletionTimestamp) || !reflect.DeepEqual(fresh.Finalizers, c.Finalizers) {
			return errConfigurationStale
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if current.ResourceVersion != nc.ResourceVersion || !reflect.DeepEqual(current.Status.Configuration, nc.Status.Configuration) {
			return errConfigurationStale
		}
		retained, err := discoveryCredentialUnsubmittedRecord(current, &fresh)
		if err != nil || !reflect.DeepEqual(record, retained) {
			return errConfigurationOwned
		}
	}
	return a.publishObservedDiscoveryConfigCompletion(ctx, c, nc, &record.Completion)
}
