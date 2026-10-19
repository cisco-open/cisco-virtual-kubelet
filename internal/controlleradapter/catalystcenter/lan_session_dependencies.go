// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"sort"
)

type lanSessionPoolBinding struct {
	role     string
	owner    Site
	evidence anycastPoolDependencies
}

// Dependency evidence deliberately excludes password material. It does not
// claim that pools have free capacity or that discovered/PnP devices are fenced.
type lanSessionDependencies struct {
	site           Site
	discoverySites []Site
	seeds          []Device
	primaryPorts   []lanLinkInterface
	pools          []lanSessionPoolBinding
}

func (c *client) lanSessionDependencies(ctx context.Context, want lanSessionIntent) (*lanSessionDependencies, error) {
	// Dependency observation works on either metadata or an ephemeral projected
	// intent without retaining the credential in its result.
	metadata := want
	metadata.Password = ""
	if err := validateLANSessionIntent(metadata, false); err != nil {
		return nil, err
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	byPath, byID := map[string]Site{}, map[string]Site{}
	for _, site := range sites {
		if !validAPIID(site.ID) {
			return nil, errInvalidResponse
		}
		byPath[site.NameHierarchy], byID[site.ID] = site, site
	}
	site, ok := byPath[want.Site]
	if !ok || site.Type == "global" {
		return nil, errConfigurationStale
	}
	result := &lanSessionDependencies{site: site}
	selectedSites := map[string]Site{}
	for _, device := range want.Devices {
		path := device.Site
		if path == "" {
			path = want.Site
		}
		target, exists := byPath[path]
		// Discovery targets must resolve to a deployable site. The provider
		// permits the session site itself only when it is not an area.
		if !exists || target.Type == "global" || target.Type == "area" {
			return nil, errConfigurationStale
		}
		selectedSites[target.ID] = target
	}
	for _, site := range selectedSites {
		result.discoverySites = append(result.discoverySites, site)
	}
	sort.Slice(result.discoverySites, func(i, j int) bool { return result.discoverySites[i].ID < result.discoverySites[j].ID })
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	primaryID := ""
	for _, ip := range []string{want.PrimaryIP, want.PeerIP} {
		if ip == "" {
			continue
		}
		device, err := resolveConfigurationDevice(configurationDeviceReference{IP: ip}, inventory)
		if err != nil {
			return nil, err
		}
		ids = append(ids, device.ID)
		if ip == want.PrimaryIP {
			primaryID = device.ID
		}
	}
	result.seeds, err = bindMulticastRPDevices(ids, inventory)
	if err != nil {
		return nil, err
	}
	if len(result.seeds) != len(ids) {
		return nil, errConfigurationDeviceAdmission
	}
	ports, err := c.lanLinkInterfaces(ctx, primaryID)
	if err != nil {
		return nil, err
	}
	for _, name := range want.Ports {
		port, err := bindLANLinkInterface(primaryID, name, ports)
		if err != nil {
			return nil, err
		}
		result.primaryPorts = append(result.primaryPorts, port)
	}
	sort.Slice(result.primaryPorts, func(i, j int) bool { return result.primaryPorts[i].ID < result.primaryPorts[j].ID })
	pools, err := c.loadAnycastPoolInventory(ctx)
	if err != nil {
		return nil, err
	}
	for _, declared := range want.Pools {
		pool, exists := pools.byName[declared.Name]
		if !exists {
			return nil, errConfigurationBusy
		}
		owner, exists := byID[pool.SiteID]
		if !exists || owner.Type == "global" || pool.Type != "LAN" {
			return nil, errConfigurationStale
		}
		// Bind the observed owner rather than guessing inheritance from a name.
		// Version-specific visibility, capacity and shared-pool eligibility are
		// separate admission checks, not established by this snapshot.
		evidence, err := pools.bindNames([]string{declared.Name}, owner.ID)
		if err != nil {
			return nil, err
		}
		result.pools = append(result.pools, lanSessionPoolBinding{role: declared.Role, owner: owner, evidence: evidence})
	}
	sort.Slice(result.pools, func(i, j int) bool { return result.pools[i].role < result.pools[j].role })
	return result, nil
}

func (c *client) revalidateLANSessionDependencies(ctx context.Context, want lanSessionIntent, before *lanSessionDependencies) error {
	if before == nil {
		return errConfigurationStale
	}
	after, err := c.lanSessionDependencies(ctx, want)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before, after) {
		return errConfigurationStale
	}
	return nil
}
