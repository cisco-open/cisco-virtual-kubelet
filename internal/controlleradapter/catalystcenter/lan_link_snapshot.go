// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
)

type lanLinkEndpoint struct {
	device Device
	port   lanLinkInterface
}

type lanLinkEndpointSnapshot struct {
	primary, secondary lanLinkEndpoint
}

// Resolve both ports from a complete native inventory. This evidence is only
// the endpoint footprint; it does not prove adjacency, free ports, pool scope,
// ownership, LAN session readiness or physical admission.
func (c *client) lanLinkEndpointSnapshot(ctx context.Context, want lanLinkIntent) (*lanLinkEndpointSnapshot, error) {
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	devices, err := bindLANLinkDevices(want, inventory)
	if err != nil {
		return nil, err
	}
	snapshot := &lanLinkEndpointSnapshot{}
	for _, device := range devices {
		name, dest := want.secondaryInterface, &snapshot.secondary
		if sameManagementIP(device.ManagementIP, want.primaryIP) {
			name, dest = want.primaryInterface, &snapshot.primary
		}
		rows, err := c.lanLinkInterfaces(ctx, device.ID)
		if err != nil {
			return nil, err
		}
		port, err := bindLANLinkInterface(device.ID, name, rows)
		if err != nil {
			return nil, err
		}
		*dest = lanLinkEndpoint{device: device, port: port}
	}
	return snapshot, nil
}

func (c *client) revalidateLANLinkEndpoints(ctx context.Context, want lanLinkIntent, before *lanLinkEndpointSnapshot) error {
	if before == nil {
		return errConfigurationStale
	}
	after, err := c.lanLinkEndpointSnapshot(ctx, want)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before, after) {
		return errConfigurationStale
	}
	return nil
}
