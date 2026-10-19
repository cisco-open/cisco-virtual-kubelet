// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

// Optional, private dependency evidence used by the existing native/managed
// observation path. Empty observers never create a v2 identity.
type wiredBulkDependencyObserver func(context.Context) (string, error)

type wiredBulkIPAMSite struct{ ID, ParentID, Path, Kind string }
type wiredBulkIPAMSnapshot struct {
	pools        []ipPool
	reservations []ipReservation
	sites        []wiredBulkIPAMSite
}

func (wiredBulkIPAMSnapshot) String() string   { return "<private bulk IPAM dependencies>" }
func (wiredBulkIPAMSnapshot) GoString() string { return "<private bulk IPAM dependencies>" }

func (s *wiredBulkIPAMSnapshot) digest() (string, error) {
	if s == nil || len(s.pools) == 0 {
		return "", errConfigurationTransition
	}
	raw, err := json.Marshal(struct {
		Version      string
		Pools        []ipPool
		Reservations []ipReservation
		Sites        []wiredBulkIPAMSite
	}{"bulk-ipam-v1", s.pools, s.reservations, s.sites})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

// Reuse the complete allocation-graph validator and typed pool/reservation
// observations shared with fabric and LAN consumers. Bind all declared native
// UUIDs, even when an identically named replacement has the same address space.
// No allocation, reservation, ownership or device mutation occurs here.
func (c *client) observeWiredBulkIPAM(ctx context.Context, intent networkReportIntent) (*wiredBulkIPAMSnapshot, error) {
	if len(intent.pools) == 0 && len(intent.reservations) == 0 {
		return nil, errConfigurationTransition
	}
	inventory, err := c.loadAnycastPoolInventory(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]ipPool{}
	for _, pool := range inventory.byID {
		byName[pool.Name] = pool
	}
	selectedPools := map[string]ipPool{}
	for _, want := range intent.pools {
		got, exists := byName[want.Name]
		if !exists {
			return nil, errConfigurationBusy
		}
		fields, err := poolSpaceDiff(want.Space, got.Space)
		if err != nil {
			return nil, err
		}
		if want.Type != got.Type || len(fields) != 0 {
			return nil, errConfigurationStale
		}
		selectedPools[got.ID] = got
	}
	result := &wiredBulkIPAMSnapshot{}
	selectedSites := map[string]wiredBulkIPAMSite{}
	if len(intent.reservations) != 0 {
		catalog, err := c.ListSites(ctx)
		if err != nil {
			return nil, err
		}
		sites := map[string]Site{}
		for _, site := range catalog {
			sites[site.NameHierarchy] = site
		}
		for _, want := range intent.reservations {
			site, exists := sites[want.site]
			if !exists {
				return nil, errConfigurationBusy
			}
			got, exists := inventory.byName[want.Name]
			if !exists {
				return nil, errConfigurationBusy
			}
			if site.Type != want.siteKind || got.SiteID != site.ID || got.Type != want.Type {
				return nil, errConfigurationStale
			}
			for i, space := range []*poolAddressSpace{want.V4, want.V6} {
				if space == nil {
					continue
				}
				observed := got.V4
				if i == 1 {
					observed = got.V6
				}
				if observed == nil {
					return nil, errConfigurationStale
				}
				parent, exists := inventory.byID[observed.GlobalPoolID]
				if !exists || parent.Name != space.GlobalPoolID {
					return nil, errConfigurationStale
				}
				fields, err := poolSpaceDiff(space, observed)
				if err != nil {
					return nil, err
				}
				if len(fields) != 0 {
					return nil, errConfigurationStale
				}
			}
			for _, space := range []*poolAddressSpace{got.V4, got.V6} {
				if space != nil {
					selectedPools[space.GlobalPoolID] = inventory.byID[space.GlobalPoolID]
				}
			}
			result.reservations = append(result.reservations, got)
			for {
				selectedSites[site.ID] = wiredBulkIPAMSite{site.ID, site.ParentID, site.NameHierarchy, site.Type}
				if site.Type == "global" {
					break
				}
				parent, exists := sites[parentSitePath(site.NameHierarchy)]
				if !exists || parent.ID != site.ParentID {
					return nil, errConfigurationStale
				}
				site = parent
			}
		}
	}
	for _, pool := range selectedPools {
		result.pools = append(result.pools, pool)
	}
	for _, site := range selectedSites {
		result.sites = append(result.sites, site)
	}
	sort.Slice(result.pools, func(i, j int) bool { return result.pools[i].ID < result.pools[j].ID })
	sort.Slice(result.reservations, func(i, j int) bool { return result.reservations[i].ID < result.reservations[j].ID })
	sort.Slice(result.sites, func(i, j int) bool { return result.sites[i].ID < result.sites[j].ID })
	return result, nil
}

func (c *client) wiredBulkDependencies(source *configSourceSnapshot) ([]wiredBulkDependencyObserver, error) {
	model, err := compileWiredBulkSource(source)
	if err != nil {
		return nil, err
	}
	if model.network == nil {
		return nil, nil
	}
	intent := *model.network
	if !wiredBulkCredentialContextSupported(&intent) {
		return nil, errWiredBulkNetworkContextUnavailable
	}
	hasIPAM := len(intent.pools) != 0 || len(intent.reservations) != 0
	hasCredentials := len(intent.assignments) != 0
	if !hasIPAM && !hasCredentials {
		return nil, nil
	}
	collect := func(ctx context.Context) (string, error) {
		allocationDigest, credentialDigest := "", ""
		if hasIPAM {
			snapshot, err := c.observeWiredBulkIPAM(ctx, intent)
			if err != nil {
				return "", err
			}
			allocationDigest, err = snapshot.digest()
			if err != nil {
				return "", err
			}
		}
		if !hasCredentials {
			return allocationDigest, nil // preserve existing allocation-only identities
		}
		snapshot, err := c.observeWiredBulkCredentials(ctx, model)
		if err != nil {
			return "", err
		}
		credentialDigest, err = snapshot.digest()
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%x", sha256.Sum256([]byte("bulk-credential-dependencies-v1:"+allocationDigest+":"+credentialDigest))), nil
	}
	return []wiredBulkDependencyObserver{func(ctx context.Context) (string, error) {
		before, err := collect(ctx)
		if err != nil {
			return "", err
		}
		after, err := collect(ctx)
		if err != nil {
			return "", err
		}
		if before != after {
			return "", errConfigurationStale
		}
		return before, nil
	}}, nil
}
