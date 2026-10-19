// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
)

var errWirelessCustomFlexInventory = errors.New("custom flex-profile inventory contract is not yet qualified")

func (c *client) wirelessProfileTagReferences(ctx context.Context, profile wirelessProfileMetadata, want wirelessProfileTag, actual *wirelessProfileTagMetadata, policy bool) (wirelessProfileTagReferences, error) {
	return c.collectWirelessProfileTagReferences(ctx, profile, want, actual, policy, false)
}

func (c *client) collectWirelessProfileTagReferences(ctx context.Context, profile wirelessProfileMetadata, want wirelessProfileTag, actual *wirelessProfileTagMetadata, policy, prospective bool) (wirelessProfileTagReferences, error) {
	refs := wirelessProfileTagReferences{profileID: profile.id, sites: map[string]string{}, zones: map[string]bool{}}
	if (!validAPIID(profile.id) && !(prospective && profile.id == "")) || profile.unmapped {
		return refs, errInvalidResponse
	}
	base := map[string]any{}
	if actual != nil {
		if actual.profileID != profile.id || actual.name != want.name || actual.unmapped {
			return refs, errConfigurationOwned
		}
		base = actual.fields
	}
	fields := mergeWirelessProfileFields(base, want.fields)
	if !policy {
		if name, _ := fields["flexProfileName"].(string); name != "" && name != "default-flex-profile" {
			return refs, errWirelessCustomFlexInventory
		}
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return refs, err
	}
	for _, site := range sites {
		if !validAPIID(site.ID) {
			return refs, errInvalidResponse
		}
		if _, exists := refs.sites[site.NameHierarchy]; exists {
			return refs, errInvalidResponse
		}
		refs.sites[site.NameHierarchy] = site.ID
	}
	refs.assigned = map[string]bool{}
	if profile.id != "" {
		refs.assigned, err = c.profileAssignedSites(ctx, profile.id)
		if err != nil {
			return refs, err
		}
	}
	if policy {
		// The appliance materializes an omitted/empty policy zone selection as
		// this built-in group. It is not a user-created AP zone with a UUID.
		refs.zones["default-zone"] = true
		zones, _ := profile.fields["apZones"].([]map[string]any)
		for _, zone := range zones {
			name, _ := zone["apZoneName"].(string)
			if !validProfileName(name) || refs.zones[name] {
				return refs, errInvalidResponse
			}
			refs.zones[name] = true
		}
	} else if name, _ := fields["apProfileName"].(string); name != "" {
		rows, err := c.wirelessAPMetadataInventory(ctx)
		if err != nil {
			return refs, err
		}
		refs.apProfiles = map[string]string{}
		for name, row := range rows {
			refs.apProfiles[name] = row.id
		}
	}
	return refs, nil
}
