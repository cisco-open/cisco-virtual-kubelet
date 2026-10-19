// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
)

type fabricEWLCLocations struct {
	primary, secondary map[string]string
	digest             string
}

// Assignment is a prerequisite, not an implicit side effect of enabling the
// embedded controller. Omitted intent retains the observed location sets;
// declared changes must converge through the existing placement executor first.
func (c *client) fabricEWLCLocations(ctx context.Context, deviceID string, want *wirelessManagedAPIntent) (*fabricEWLCLocations, error) {
	if !validAPIID(deviceID) {
		return nil, errInvalidResponse
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	primary, err := c.wirelessManagedAPLocations(ctx, deviceID, "primary")
	if err != nil {
		return nil, err
	}
	secondary, err := c.wirelessManagedAPLocations(ctx, deviceID, "secondary")
	if err != nil {
		return nil, err
	}
	return resolveFabricEWLCLocations(deviceID, want, sites, primary, secondary)
}

func resolveFabricEWLCLocations(deviceID string, want *wirelessManagedAPIntent, sites []Site, primary, secondary map[string]string) (*fabricEWLCLocations, error) {
	if !validAPIID(deviceID) || want != nil && want.anchor != nil {
		return nil, errInvalidResponse
	}
	index := map[string]string{}
	ids := map[string]bool{}
	for _, site := range sites {
		if !validAPIID(site.ID) || !validSitePath(site.NameHierarchy) || index[site.NameHierarchy] != "" || ids[site.ID] {
			return nil, errInvalidResponse
		}
		index[site.NameHierarchy] = site.ID
		ids[site.ID] = true
	}
	intent := wirelessManagedAPIntent{}
	if want != nil {
		intent = *want
	}
	out := &fabricEWLCLocations{primary: map[string]string{}, secondary: map[string]string{}}
	for _, set := range []struct {
		desired            []string
		observed, retained map[string]string
	}{
		{intent.primary, primary, out.primary}, {intent.secondary, secondary, out.secondary},
	} {
		actual, err := wirelessManagedAPSet(nil, set.observed, index)
		if err != nil {
			return nil, err
		}
		desired, err := wirelessManagedAPSet(set.desired, set.observed, index)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(actual, desired) {
			return nil, errConfigurationBusy
		}
		for path, id := range set.observed {
			set.retained[path] = id
		}
	}
	digest, err := wirelessTagDigest(map[string]any{"device": deviceID, "primary": out.primary, "secondary": out.secondary})
	if err != nil {
		return nil, err
	}
	out.digest = digest
	return out, nil
}
