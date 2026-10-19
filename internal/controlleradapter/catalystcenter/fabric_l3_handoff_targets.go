// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"

	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Bind the complete observed fabric footprint to immutable managed identities.
// This produces no write object, reservation or submission authority. Both the
// native and Kubernetes evidence must remain stable throughout collection.
func (c *client) fabricL3IPHandoffTargets(ctx context.Context, reader ctrlclient.Reader, namespace string, want fabricL3IPHandoffDeploymentIntent) (*fabricL3IPHandoffDependencies, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*fabricL3IPHandoffDependencies], error) {
		deps, err := c.fabricL3IPHandoffDependencies(ctx, want.handoff, want.device)
		if err != nil {
			return configurationPhysicalObservation[*fabricL3IPHandoffDependencies]{}, err
		}
		return configurationPhysicalObservation[*fabricL3IPHandoffDependencies]{state: deps, physical: deps.physical, digest: deps.digest}, nil
	})
}

// This planning path deliberately carries a denying guard. Only a separately
// admitted dispatch may construct a write with submission authority.
func (c *client) planManagedFabricL3IPHandoff(ctx context.Context, reader ctrlclient.Reader, namespace string, want fabricL3IPHandoffDeploymentIntent) (*configurationWrite, []contract.GroupTargetIdentity, error) {
	deps, targets, err := c.fabricL3IPHandoffTargets(ctx, reader, namespace, want)
	if err != nil {
		return nil, nil, err
	}
	write, err := c.planGuardedFabricL3IPHandoffCreate(ctx, want.handoff, want.device, func(context.Context) error { return errConfigurationDeviceAdmission })
	if err != nil {
		return nil, nil, err
	}
	if write != nil && (write.resource != fabricL3IPHandoffResource(deps) || write.identity.target != deps.binding.desired.DeviceID || write.identity.parent != deps.binding.desired.FabricID) {
		return nil, nil, errConfigurationStale
	}
	fresh, current, err := c.fabricL3IPHandoffTargets(ctx, reader, namespace, want)
	if err != nil {
		return nil, nil, err
	}
	if fresh.digest != deps.digest || !reflect.DeepEqual(targets, current) {
		return nil, nil, errConfigurationStale
	}
	return write, targets, nil
}
