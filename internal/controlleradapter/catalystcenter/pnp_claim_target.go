// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"sort"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

type pnpClaimTarget struct {
	device  pnpDevice
	serials []string
}

// Require all declared stack members to exist on the single registered record.
// Retaining state/workflow observations permits later change detection without
// inventing native readiness semantics or treating registration as admission.
func (c *client) pnpClaimTarget(ctx context.Context, want pnpDeviceIntent, deviceID string) (*pnpClaimTarget, error) {
	serials, err := validatePnPDeviceIntent(want)
	if err != nil {
		return nil, err
	}
	if !validAPIID(deviceID) {
		return nil, errInvalidResponse
	}
	rows, err := c.pnpDevices(ctx)
	if err != nil {
		return nil, err
	}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	selected, err := bindPnPPhysicalDevices(serials, rows, inventory)
	if err != nil {
		return nil, err
	}
	if len(selected) != 1 || selected[0].ID != deviceID {
		return nil, errConfigurationDeviceAdmission
	}
	row := selected[0]
	if !pnpRegistrationMatches(pnpRegistrationReceipt{deviceID: deviceID, serial: serials[0], pid: want.PID, hostname: want.Name, stack: len(serials) > 1}, row) {
		return nil, errConfigurationStale
	}
	sort.Strings(serials)
	return &pnpClaimTarget{device: row, serials: serials}, nil
}

// Bind the physical snapshot to retained registration ownership and the current
// manifest. This does not reserve hardware or submit the native site-claim API.
func (a *adapter) observeOwnedPnPClaimTarget(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want pnpDeviceIntent) (*pnpClaimTarget, error) {
	if a == nil {
		return nil, errConfigurationTransition
	}
	before, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	id, err := a.resolveOwnedPnPRegistration(ctx, config, source, want)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, errConfigurationOwned
	}
	target, err := a.client.pnpClaimTarget(ctx, want, id)
	if err != nil {
		return nil, err
	}
	confirmed, err := a.client.pnpClaimTarget(ctx, want, id)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(target, confirmed) {
		return nil, errConfigurationStale
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	after, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if before.UID != after.UID || before.ResourceVersion != after.ResourceVersion {
		return nil, errConfigurationStale
	}
	return target, nil
}
