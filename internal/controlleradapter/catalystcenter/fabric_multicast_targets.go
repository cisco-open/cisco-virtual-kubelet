// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"

	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func (c *client) multicastVNTargets(ctx context.Context, reader ctrlclient.Reader, namespace, path string, wants []fabricMulticastVNIntent) (*fabricMulticastSnapshot, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*fabricMulticastSnapshot], error) {
		state, err := c.multicastSnapshot(ctx, path, wants)
		if err != nil {
			return configurationPhysicalObservation[*fabricMulticastSnapshot]{}, err
		}
		return configurationPhysicalObservation[*fabricMulticastSnapshot]{state: state, physical: state.devices, digest: state.digest}, nil
	})
}

// Preparation binds every fabric member, not only RPs, through the common
// repeated native/managed observer. Its returned closure cannot submit.
func (c *client) planManagedFabricMulticastVN(ctx context.Context, reader ctrlclient.Reader, namespace, path string, wants []fabricMulticastVNIntent, name string) (*configurationWrite, []contract.GroupTargetIdentity, error) {
	before, targets, err := c.multicastVNTargets(ctx, reader, namespace, path, wants)
	if err != nil {
		return nil, nil, err
	}
	write, err := c.planGuardedFabricMulticastVN(ctx, path, wants, name, func(context.Context) error { return errConfigurationDeviceAdmission })
	if err != nil {
		return nil, nil, err
	}
	action, target := "Create", before.networkIDs[name]
	if _, exists := before.before[name]; exists {
		action = "Update"
	}
	if write != nil && (write.action != action || write.identity.target != target || write.resource != multicastVNOperationResource(path, name, before) || write.identity.parent != before.fabricID) {
		return nil, nil, errConfigurationStale
	}
	after, current, err := c.multicastVNTargets(ctx, reader, namespace, path, wants)
	if err != nil {
		return nil, nil, err
	}
	if before.digest != after.digest || !reflect.DeepEqual(targets, current) {
		return nil, nil, errConfigurationStale
	}
	return write, targets, nil
}
