// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"reflect"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func (c *client) fabricSDAHandoffTargets(ctx context.Context, reader ctrlclient.Reader, namespace string, want fabricSDAHandoffDeploymentIntent) (*fabricSDAHandoffDependencies, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*fabricSDAHandoffDependencies], error) {
		deps, err := c.fabricSDAHandoffDependencies(ctx, want)
		if err != nil {
			return configurationPhysicalObservation[*fabricSDAHandoffDependencies]{}, err
		}
		return configurationPhysicalObservation[*fabricSDAHandoffDependencies]{state: deps, physical: deps.physical, digest: deps.digest}, nil
	})
}

// This planning path deliberately carries a denying guard. Only a separately
// admitted dispatch may construct a write with submission authority.
func (c *client) planManagedFabricSDAHandoff(ctx context.Context, reader ctrlclient.Reader, namespace string, want fabricSDAHandoffDeploymentIntent) (*configurationWrite, []contract.GroupTargetIdentity, error) {
	deps, targets, err := c.fabricSDAHandoffTargets(ctx, reader, namespace, want)
	if err != nil {
		return nil, nil, err
	}
	write, err := c.planGuardedFabricSDAHandoffCreate(ctx, want, func(context.Context) error { return errConfigurationDeviceAdmission })
	if err != nil {
		return nil, nil, err
	}
	if write != nil && (write.resource != fabricSDAHandoffResource(deps) || write.identity.target != deps.binding.desired.DeviceID || write.identity.parent != deps.binding.desired.FabricID) {
		return nil, nil, errConfigurationStale
	}
	fresh, current, err := c.fabricSDAHandoffTargets(ctx, reader, namespace, want)
	if err != nil {
		return nil, nil, err
	}
	if fresh.digest != deps.digest || !reflect.DeepEqual(targets, current) {
		return nil, nil, errConfigurationStale
	}
	return write, targets, nil
}
