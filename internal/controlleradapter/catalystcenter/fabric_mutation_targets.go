// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "sort"

// Return the complete device footprint for a fabric-wide mutation. Eligibility
// checks (such as RP roles) must not reduce this set. Inventory evidence is not a
// dispatch permit: runtime still needs reservations for every returned device.
func bindFabricMutationTargets(fabricID string, memberships map[string]fabricDeviceMembership, inventory []Device) ([]fabricDeviceMembership, []Device, error) {
	if !validAPIID(fabricID) {
		return nil, nil, errInvalidResponse
	}
	ids := make([]string, 0, len(memberships))
	for id, member := range memberships {
		if !validAPIID(member.ID) || member.NetworkDeviceID != id || member.FabricID != fabricID {
			return nil, nil, errConfigurationStale
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	devices, err := bindMulticastRPDevices(ids, inventory)
	if err != nil {
		return nil, nil, err
	}
	members := make([]fabricDeviceMembership, 0, len(ids))
	for _, id := range ids {
		members = append(members, memberships[id])
	}
	return members, devices, nil
}
