// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "context"

type accessPointDependencies struct {
	target               accessPointConfigurationTarget
	siteID, rfID, digest string
	assigned             bool
}

type accessPointDependencyCatalog struct {
	inventory []Device
	sites     []Site
	profiles  map[string]wirelessRFMetadata
}

func (c *client) accessPointDependencyCatalog(ctx context.Context) (*accessPointDependencyCatalog, error) {
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	profiles, err := c.wirelessRFProfiles(ctx)
	if err != nil {
		return nil, err
	}
	return &accessPointDependencyCatalog{inventory: inventory, sites: sites, profiles: profiles}, nil
}

// Read prerequisite evidence using the same inventory, site, RF and assignment
// readers as other controller operations. This is not a submission grant.
// The digest pins replacement-sensitive identities and observed RF attributes;
// callers must retain/recheck it around any future physical admission handshake.
func (c *client) accessPointProvisionDependencies(ctx context.Context, want accessPointDeviceIntent) (accessPointDependencies, error) {
	if want.state != "PROVISION" && want.state != "REPROVISION" || want.rfProfile == "" {
		return accessPointDependencies{}, errAccessPointModel
	}
	catalog, err := c.accessPointDependencyCatalog(ctx)
	if err != nil {
		return accessPointDependencies{}, err
	}
	result, err := catalog.resolve(want)
	if err != nil {
		return accessPointDependencies{}, err
	}
	assignments, err := c.assignedDevices(ctx, result.siteID)
	if err != nil {
		return accessPointDependencies{}, err
	}
	result.assigned = assignments[result.target.device.ID]
	return result, nil
}

// One complete native catalog serves a single observation only. Cohort callers
// share it across members, then collect a new catalog for every revalidation.
func (catalog *accessPointDependencyCatalog) resolve(want accessPointDeviceIntent) (accessPointDependencies, error) {
	var result accessPointDependencies
	if want.state != "PROVISION" && want.state != "REPROVISION" {
		return result, errAccessPointModel
	}
	if want.rfProfile == "" {
		return result, errAccessPointModel
	}
	target, err := resolveAccessPointConfigurationTarget(want.inventory.reference, catalog.inventory)
	if err != nil {
		return result, err
	}
	siteID := ""
	for _, site := range catalog.sites {
		if site.NameHierarchy == want.inventory.site {
			if site.Type != "floor" {
				return result, errAccessPointModel
			}
			siteID = site.ID
		}
	}
	if !validAPIID(siteID) {
		return result, errInvalidResponse
	}
	rf, found := catalog.profiles[want.rfProfile]
	if !found || !validAPIID(rf.id) || rf.unknownFields {
		return result, errInvalidResponse
	}
	digest, err := wirelessTagDigest(map[string]any{"device": target.device.ID, "serial": target.device.Serial, "ethernetMAC": target.ethernetMAC, "site": siteID, "rf": rf.id, "rfFields": rf.fields})
	if err != nil {
		return result, err
	}
	return accessPointDependencies{target: target, siteID: siteID, rfID: rf.id, digest: digest}, nil
}
