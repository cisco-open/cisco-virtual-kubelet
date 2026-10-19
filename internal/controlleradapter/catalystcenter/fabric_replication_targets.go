// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"reflect"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func (c *client) replicationTargets(ctx context.Context, reader ctrlclient.Reader, namespace, path string) (*fabricReplicationDependencies, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*fabricReplicationDependencies], error) {
		deps, err := c.replicationDependencies(ctx, path)
		if err != nil {
			return configurationPhysicalObservation[*fabricReplicationDependencies]{}, err
		}
		return configurationPhysicalObservation[*fabricReplicationDependencies]{state: deps, physical: deps.physical, digest: deps.digest}, nil
	})
}

func (c *client) planManagedFabricReplication(ctx context.Context, reader ctrlclient.Reader, namespace, path, mode string) (*configurationWrite, []contract.GroupTargetIdentity, error) {
	before, targets, err := c.replicationTargets(ctx, reader, namespace, path)
	if err != nil {
		return nil, nil, err
	}
	write, err := c.planGuardedFabricReplicationGroup(ctx, path, mode, func(context.Context) error { return errConfigurationDeviceAdmission })
	if err != nil {
		return nil, nil, err
	}
	if write != nil && (write.resource != fabricReplicationGroupResource(path, mode, before) || write.identity.target != before.state.fabricID || write.identity.parent != before.state.siteID) {
		return nil, nil, errConfigurationStale
	}
	after, current, err := c.replicationTargets(ctx, reader, namespace, path)
	if err != nil {
		return nil, nil, err
	}
	if before.digest != after.digest || !reflect.DeepEqual(targets, current) {
		return nil, nil, errConfigurationStale
	}
	return write, targets, nil
}
