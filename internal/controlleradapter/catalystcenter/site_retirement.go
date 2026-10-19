// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
)

var errSiteRetirementReferenced = errors.New("site retirement dependencies are present or unavailable")

// This observation is a prerequisite, not deletion authority. A caller must
// independently establish source, section ownership, private native baseline,
// current journal identity and retained completion before enabling public Delete.
// Native attributes remain in memory, never in a public operation resource.
type siteRetirementObservation struct {
	target Site
	native []byte
}

func (siteRetirementObservation) String() string   { return "<site retirement observation>" }
func (siteRetirementObservation) GoString() string { return "<site retirement observation>" }

func (c *client) observeUnusedSiteLeaf(ctx context.Context, expected Site) (*siteRetirementObservation, error) {
	if !canonicalTemplateUUID(expected.ID) || !validSitePath(expected.NameHierarchy) || expected.NameHierarchy == "Global" || expected.Type != "area" && expected.Type != "building" && expected.Type != "floor" {
		return nil, errSiteRetirementReferenced
	}
	reader := c.profileSitePruneReader()
	if err := reader.loadSites(ctx); err != nil {
		return nil, err
	}
	target, ok := reader.sites[expected.ID]
	if !ok || !reflect.DeepEqual(target, expected) {
		return nil, errConfigurationStale
	}
	actual := make([]Site, 0, len(reader.sites))
	for _, site := range reader.sites {
		if strings.HasPrefix(site.NameHierarchy, target.NameHierarchy+"/") {
			return nil, errSiteRetirementReferenced
		}
		actual = append(actual, site)
	}
	sort.Slice(actual, func(i, j int) bool { return actual[i].NameHierarchy < actual[j].NameHierarchy })
	if err := reader.unusedScope(ctx, target.NameHierarchy); err != nil {
		return nil, err
	}
	if target.Type == "floor" {
		aps, err := c.plannedAPNativePositions(ctx, target.ID)
		if err != nil || len(aps) != 0 {
			return nil, errSiteRetirementReferenced
		}
	}
	// Use the existing complete profile inventories and assignment readers. A
	// dangling foreign site ID is incomplete evidence, not an unrelated reference.
	summaries, err := c.listNetworkProfileSummaries(ctx)
	if err != nil {
		return nil, err
	}
	wireless, err := c.wirelessProfileInventory(ctx)
	if err != nil {
		return nil, err
	}
	profiles := map[string]bool{}
	namespaces := map[string]string{}
	for _, p := range summaries {
		profiles[p.ID] = true
		namespaces[p.ID] = p.Namespace
	}
	for _, p := range wireless {
		if profiles[p.id] && namespaces[p.id] != "wireless" || p.unmapped {
			return nil, errSiteRetirementReferenced
		}
		profiles[p.id] = true
		for _, policy := range []bool{false, true} {
			tags, err := c.wirelessProfileTagInventory(ctx, p.id, policy)
			if err != nil {
				return nil, err
			}
			for _, tag := range tags {
				if tag.unmapped {
					return nil, errSiteRetirementReferenced
				}
				ids, ok := tag.fields["siteIds"].([]string)
				if !ok {
					return nil, errSiteRetirementReferenced
				}
				for _, id := range ids {
					if _, ok := reader.sites[id]; !ok || id == target.ID {
						return nil, errSiteRetirementReferenced
					}
				}
			}
		}
	}
	ids := make([]string, 0, len(profiles))
	for id := range profiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		assigned, err := c.profileAssignedSites(ctx, id)
		if err != nil {
			return nil, err
		}
		for siteID := range assigned {
			if _, ok := reader.sites[siteID]; !ok || siteID == target.ID {
				return nil, errSiteRetirementReferenced
			}
		}
	}
	fabrics, err := c.fabricSites(ctx)
	if err != nil {
		return nil, err
	}
	for id := range fabrics {
		if _, ok := reader.sites[id]; !ok || id == target.ID {
			return nil, errSiteRetirementReferenced
		}
	}
	zones, err := c.fabricZones(ctx)
	if err != nil {
		return nil, err
	}
	for id := range zones {
		if _, ok := reader.sites[id]; !ok || id == target.ID {
			return nil, errSiteRetirementReferenced
		}
	}
	reservations, err := c.listIPReservations(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range reservations {
		if _, ok := reader.sites[r.SiteID]; !ok || r.SiteID == target.ID {
			return nil, errSiteRetirementReferenced
		}
	}
	if err := c.checkSiteLANAutomation(ctx, target, reader.sites); err != nil {
		return nil, err
	}
	native, err := c.siteNativeFootprint(ctx, actual, target.NameHierarchy, true)
	if err != nil {
		return nil, err
	}
	local, err := c.siteEmptyLocalConfiguration(ctx, target.ID)
	if err != nil {
		return nil, err
	}
	// Bind absence shapes as well as site attributes. Even an inherited/empty
	// transition must replan rather than silently changing the preflight proof.
	native, err = json.Marshal(struct {
		Site  json.RawMessage
		Local map[string]map[string]string
	}{native, local})
	if err != nil {
		return nil, errConfigurationTransition
	}
	return &siteRetirementObservation{target: target, native: native}, nil
}

// Two fresh observations avoid treating a stale cached dependency read or native
// baseline as an admission result. This helper never submits or retries a write.
func (c *client) stableUnusedSiteLeaf(ctx context.Context, expected Site) error {
	first, err := c.observeUnusedSiteLeaf(ctx, expected)
	if err != nil {
		return err
	}
	second, err := c.observeUnusedSiteLeaf(ctx, expected)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(first, second) {
		return errConfigurationStale
	}
	return nil
}
