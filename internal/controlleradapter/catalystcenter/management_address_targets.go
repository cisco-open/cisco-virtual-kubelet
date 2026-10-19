// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/netip"
	"reflect"

	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type managementAddressSnapshot struct {
	native    Device
	change    *managementAddressChange
	requested string
}

// Stage the native write only after the old endpoint resolves to the current
// managed incarnation. The supplied guard must still provide the real shared
// reservation permit and independently authenticated destination proof.
// This helper neither creates that permit nor changes a declaration.
func (c *client) planManagedManagementAddressChange(ctx context.Context, reader ctrlclient.Reader, namespace string, ref configurationDeviceReference, guard managementAddressGuard) (*configurationWrite, []contract.GroupTargetIdentity, error) {
	if guard == nil {
		return nil, nil, errConfigurationDeviceAdmission
	}
	before, targets, err := c.managementAddressTargets(ctx, reader, namespace, ref)
	if err != nil {
		return nil, nil, err
	}
	if before.change == nil {
		return nil, targets, nil
	}
	check := func(ctx context.Context) error {
		latest, currentTargets, err := c.managementAddressTargets(ctx, reader, namespace, ref)
		if err != nil {
			return err
		}
		if latest.change == nil || !reflect.DeepEqual(before, latest) || !reflect.DeepEqual(targets, currentTargets) {
			return errConfigurationStale
		}
		return nil
	}
	write, err := c.planGuardedManagementAddressChange(ctx, ref, func(ctx context.Context, target Device, requested string) error {
		if err := check(ctx); err != nil {
			return err
		}
		if err := guard(ctx, target, requested); err != nil {
			return err
		}
		return check(ctx)
	})
	if err != nil || write == nil {
		if err == nil {
			err = errConfigurationStale
		}
		return nil, nil, err
	}
	return write, targets, nil
}

// Capture the old endpoint against the existing managed device and Node
// incarnation. Inventory planning, including a no-op, never permits adopting
// a CiscoDevice, changing its address or obtaining a mutation lease.
func (c *client) managementAddressTargets(ctx context.Context, reader ctrlclient.Reader, namespace string, ref configurationDeviceReference) (*managementAddressSnapshot, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*managementAddressSnapshot], error) {
		var empty configurationPhysicalObservation[*managementAddressSnapshot]
		inventory, err := c.configurationDevices(ctx)
		if err != nil {
			return empty, err
		}
		change, err := planManagementAddressChange(ref, inventory)
		if err != nil {
			return empty, err
		}
		identity := ref
		identity.IP = ""
		target, err := resolveConfigurationDevice(identity, inventory)
		if err != nil {
			return empty, err
		}
		requested, err := netip.ParseAddr(ref.IP)
		if err != nil {
			return empty, errInvalidResponse
		}
		snapshot := &managementAddressSnapshot{native: target, change: change, requested: requested.String()}
		raw, err := json.Marshal([]string{ref.Name, ref.FQDN, ref.Serial, target.ID, target.Serial, target.ManagementIP, snapshot.requested})
		if err != nil {
			return empty, err
		}
		return configurationPhysicalObservation[*managementAddressSnapshot]{state: snapshot, physical: []Device{target}, digest: fmt.Sprintf("%x", sha256.Sum256(raw))}, nil
	})
}
