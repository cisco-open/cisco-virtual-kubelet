// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"

	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Resolve the entire fabric footprint through the shared repeated observer.
// These identities are preparation evidence only, never dispatch authority.
func (c *client) fabricEWLCTargets(ctx context.Context, reader ctrlclient.Reader, namespace, fabricID string) (*fabricEWLCScope, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*fabricEWLCScope], error) {
		scope, err := c.fabricEWLCScope(ctx, fabricID)
		if err != nil {
			return configurationPhysicalObservation[*fabricEWLCScope]{}, err
		}
		return configurationPhysicalObservation[*fabricEWLCScope]{state: scope, physical: scope.physical, digest: scope.digest}, nil
	})
}

// Preparation deliberately receives an unusable submit callback. The durable
// group coordinator must rebuild the write with its journal-bound dispatch permit.
func (c *client) planManagedFabricEWLC(ctx context.Context, reader ctrlclient.Reader, namespace string, want fabricEWLCIntent) (*configurationWrite, []contract.GroupTargetIdentity, error) {
	before, targets, err := c.fabricEWLCDependencyTargets(ctx, reader, namespace, want)
	if err != nil {
		return nil, nil, err
	}
	write, err := c.planGuardedFabricEWLC(ctx, want, func(context.Context) error { return errConfigurationDeviceAdmission })
	if err != nil {
		return nil, nil, err
	}
	payload, err := fabricEWLCPayload(before.target.ID, want.settings)
	if err != nil {
		return nil, nil, err
	}
	if write != nil && (write.resource != fabricEWLCResource(before, payload) || write.identity.target != before.target.ID || write.identity.parent != before.scope.fabricID) {
		return nil, nil, errConfigurationStale
	}
	after, current, err := c.fabricEWLCDependencyTargets(ctx, reader, namespace, want)
	if err != nil {
		return nil, nil, err
	}
	if before.digest != after.digest || !reflect.DeepEqual(targets, current) {
		return nil, nil, errConfigurationStale
	}
	return write, targets, nil
}

// Bind the composed prerequisites, including zone-parent populations and AP
// locations, rather than using the narrower native scope for future dispatch.
func (c *client) fabricEWLCDependencyTargets(ctx context.Context, reader ctrlclient.Reader, namespace string, want fabricEWLCIntent) (*fabricEWLCFabricDependencies, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*fabricEWLCFabricDependencies], error) {
		deps, err := c.fabricEWLCFabricDependencies(ctx, want)
		if err != nil {
			return configurationPhysicalObservation[*fabricEWLCFabricDependencies]{}, err
		}
		return configurationPhysicalObservation[*fabricEWLCFabricDependencies]{state: deps, physical: deps.foundation.physical, digest: deps.digest}, nil
	})
}
