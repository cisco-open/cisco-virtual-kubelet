// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"sort"
)

type lanLinkDiscoveryOrigin struct {
	SessionID string
	Site      string
	Action    string
	Status    string
	Created   string
	Device    lanDiscoveredDevice
}

type lanLinkDiscoveryScope struct {
	site    Site
	origins []lanLinkDiscoveryOrigin
}

type lanLinkPoolDependencies struct {
	primary, secondary lanLinkDiscoveryScope
	pool               anycastPoolDependencies
}

// Discover the historical origin by physical serial and recorded management IP,
// never by current inventory placement or by the session's seed IP. Repeated
// origins at one site are retained; conflicting sites/address aliases stay held.
// STOP and status text are metadata, not proof that a session finished safely.
func bindLANLinkDiscoveryScope(endpoint lanLinkEndpoint, sessions []lanSession, sites map[string]Site) (lanLinkDiscoveryScope, error) {
	var result lanLinkDiscoveryScope
	for _, session := range sessions {
		for _, device := range session.Discovered {
			address := false
			for _, ip := range device.Addresses {
				address = address || sameManagementIP(ip, endpoint.device.ManagementIP)
			}
			if device.Serial != endpoint.device.Serial {
				if address {
					return result, errConfigurationStale
				}
				continue
			}
			if !address {
				return result, errConfigurationStale
			}
			site, exists := sites[session.Site]
			if !exists || !validAPIID(site.ID) || site.Type == "global" {
				return result, errConfigurationStale
			}
			if result.site.ID != "" && result.site.ID != site.ID {
				return result, errConfigurationStale
			}
			result.site = site
			result.origins = append(result.origins, lanLinkDiscoveryOrigin{SessionID: session.ID, Site: session.Site, Action: session.Action, Status: session.Status, Created: session.Created, Device: device})
		}
	}
	if len(result.origins) == 0 {
		return result, errConfigurationBusy
	}
	sort.Slice(result.origins, func(i, j int) bool { return result.origins[i].SessionID < result.origins[j].SessionID })
	return result, nil
}

// Bounded same-discovery-site IPv4 LAN pool binding. Reuse complete IPAM parent
// and reservation validation; cross-site/shared visibility and capacity require
// separate qualified semantics. This snapshot grants no link execution authority.
func (c *client) lanLinkPoolDependencies(ctx context.Context, want lanLinkIntent, endpoints *lanLinkEndpointSnapshot) (*lanLinkPoolDependencies, error) {
	if err := want.validate(); err != nil {
		return nil, err
	}
	if endpoints == nil || want.pool == "" || !sameManagementIP(want.primaryIP, endpoints.primary.device.ManagementIP) || !sameManagementIP(want.secondaryIP, endpoints.secondary.device.ManagementIP) {
		return nil, errConfigurationTransition
	}
	sessions, err := c.lanSessions(ctx)
	if err != nil {
		return nil, err
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	byPath := map[string]Site{}
	for _, site := range sites {
		byPath[site.NameHierarchy] = site
	}
	primary, err := bindLANLinkDiscoveryScope(endpoints.primary, sessions, byPath)
	if err != nil {
		return nil, err
	}
	secondary, err := bindLANLinkDiscoveryScope(endpoints.secondary, sessions, byPath)
	if err != nil {
		return nil, err
	}
	if primary.site.ID != secondary.site.ID {
		return nil, errConfigurationTransition
	}
	inventory, err := c.loadAnycastPoolInventory(ctx)
	if err != nil {
		return nil, err
	}
	pool, exists := inventory.byName[want.pool]
	if !exists {
		return nil, errConfigurationBusy
	}
	if pool.Type != "LAN" || pool.V4 == nil {
		return nil, errConfigurationTransition
	}
	evidence, err := inventory.bindNames([]string{want.pool}, primary.site.ID)
	if err != nil {
		return nil, err
	}
	return &lanLinkPoolDependencies{primary: primary, secondary: secondary, pool: evidence}, nil
}

func (c *client) revalidateLANLinkPoolDependencies(ctx context.Context, want lanLinkIntent, endpoints *lanLinkEndpointSnapshot, before *lanLinkPoolDependencies) error {
	if before == nil {
		return errConfigurationStale
	}
	after, err := c.lanLinkPoolDependencies(ctx, want, endpoints)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before, after) {
		return errConfigurationStale
	}
	return nil
}
