// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"

	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type fabricWirelessMulticastPhysical struct {
	dependencies *fabricWirelessMulticastDependencies
	physical     []Device
	digest       string
}

// Resolve native fabric UUIDs to canonical physical serials before asking the
// shared managed-target resolver for immutable CiscoDevice/Node identities.
func (c *client) wirelessMulticastPhysical(ctx context.Context, path string) (*fabricWirelessMulticastPhysical, error) {
	deps, err := c.wirelessMulticastDependencies(ctx, path)
	if err != nil {
		return nil, err
	}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(deps.members))
	for _, member := range deps.members {
		ids = append(ids, member.NetworkDeviceID)
	}
	physical, err := bindMulticastRPDevices(ids, inventory)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal([]any{deps.digest, physical})
	if err != nil {
		return nil, err
	}
	return &fabricWirelessMulticastPhysical{dependencies: deps, physical: physical, digest: fmt.Sprintf("%x", sha256.Sum256(raw))}, nil
}

func (c *client) wirelessMulticastTargets(ctx context.Context, reader ctrlclient.Reader, namespace, path string) (*fabricWirelessMulticastPhysical, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*fabricWirelessMulticastPhysical], error) {
		state, err := c.wirelessMulticastPhysical(ctx, path)
		if err != nil {
			return configurationPhysicalObservation[*fabricWirelessMulticastPhysical]{}, err
		}
		return configurationPhysicalObservation[*fabricWirelessMulticastPhysical]{state: state, physical: state.physical, digest: state.digest}, nil
	})
}

// Preparation can inspect a native plan but cannot submit it. Dispatch must
// rebuild it with authority obtained from the retained physical group journal.
func (c *client) planManagedFabricWirelessMulticast(ctx context.Context, reader ctrlclient.Reader, namespace, path string, enabled bool) (*configurationWrite, []contract.GroupTargetIdentity, error) {
	before, targets, err := c.wirelessMulticastTargets(ctx, reader, namespace, path)
	if err != nil {
		return nil, nil, err
	}
	write, err := c.planGuardedFabricWirelessMulticast(ctx, path, enabled, func(context.Context) error { return errConfigurationDeviceAdmission })
	if err != nil {
		return nil, nil, err
	}
	deps := before.dependencies
	if write != nil && (write.resource != fabricWirelessMulticastResource(path, enabled, deps) || write.identity.target != deps.state.fabricID || write.identity.parent != deps.state.siteID) {
		return nil, nil, errConfigurationStale
	}
	after, current, err := c.wirelessMulticastTargets(ctx, reader, namespace, path)
	if err != nil {
		return nil, nil, err
	}
	if before.digest != after.digest || !reflect.DeepEqual(targets, current) {
		return nil, nil, errConfigurationStale
	}
	return write, targets, nil
}
