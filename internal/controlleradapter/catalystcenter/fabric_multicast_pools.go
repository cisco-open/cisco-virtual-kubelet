// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "context"

// Reuse IPAM's complete allocation validation and UUID/content digest. The caller
// derives reservationSiteID from fabric topology, never from a pool-name guess.
func (c *client) multicastPoolDependencies(ctx context.Context, want fabricMulticastVNIntent, reservationSiteID string, observed *fabricMulticastVN) (anycastPoolDependencies, error) {
	inventory, err := c.loadAnycastPoolInventory(ctx)
	if err != nil {
		return anycastPoolDependencies{}, err
	}
	return inventory.bindMulticastPool(want, reservationSiteID, observed)
}
func (inventory *anycastPoolInventory) bindMulticastPool(want fabricMulticastVNIntent, reservationSiteID string, observed *fabricMulticastVN) (anycastPoolDependencies, error) {
	zero := anycastPoolDependencies{}
	deps, err := inventory.bindNames([]string{want.pool}, reservationSiteID)
	if err != nil {
		return zero, err
	}
	prefix, err := poolPrefix(deps.primary.V4)
	if err != nil || !prefix.Addr().Is4() {
		return zero, errInvalidResponse
	}
	dual := deps.primary.V6 != nil
	if dual {
		prefix, err = poolPrefix(deps.primary.V6)
		if err != nil || prefix.Addr().Is4() {
			return zero, errInvalidResponse
		}
	}
	check := func(rp map[string]any, devices int) error {
		ranges, _ := rp["ipv6AsmRanges"].([]string)
		if !dual && (rp["ipv6Address"] != nil || rp["isDefaultV6RP"] == true || len(ranges) > 0) {
			return errConfigurationTransition
		}
		// The native contract allows only one fabric RP device with a dual-stack pool.
		if dual && rp["rpDeviceLocation"] == "FABRIC" && devices > 1 {
			return errConfigurationTransition
		}
		return nil
	}
	for _, rp := range want.rps {
		if err := check(rp.payload, len(rp.devices)); err != nil {
			return zero, err
		}
	}
	if observed != nil {
		if observed.unknownFields || observed.Pool != want.pool || observed.Network != want.name {
			return zero, errConfigurationStale
		}
		rps, known := observed.fields["multicastRPs"].([]map[string]any)
		if !known {
			return zero, errInvalidResponse
		}
		for _, rp := range rps {
			ids, _ := rp["networkDeviceIds"].([]string)
			if err := check(rp, len(ids)); err != nil {
				return zero, err
			}
		}
	}
	return deps, nil
}
