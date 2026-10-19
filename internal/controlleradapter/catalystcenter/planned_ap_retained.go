// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Retain removes intent, not the recorded identity or private-state boundary.
// Resolve every retained AP through its recorded floor UUID before querying any
// AP collection. The native name must reproduce the original logical key; a
// matching UUID under a renamed or replacement hierarchy cannot be adopted.
// This is observation authority only, never creation or deletion authority.
func (e *plannedAPExecutor) retainedPrivateView(ctx context.Context, sites []Site) ([]byte, error) {
	if e.comparison == nil {
		return nil, nil
	}
	keys := make([]string, 0, len(e.identities))
	floors := map[string]Site{}
	for _, site := range sites {
		floors[site.ID] = site
	}
	for key, record := range e.identities {
		floor, found := floors[record.receipt.Operation.ParentID]
		if !found || floor.Type != "floor" || record.receipt.Operation.ConfigUID != e.identityOwner || record.keyID == "" {
			return nil, errConfigurationOwned
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	inventories := map[string]map[string]plannedAPNativePosition{}
	views := make([]json.RawMessage, 0, len(keys))
	for _, key := range keys {
		record := e.identities[key]
		floor := floors[record.receipt.Operation.ParentID]
		inventory, seen := inventories[floor.ID]
		if !seen {
			var err error
			inventory, err = e.client.plannedAPNativePositions(ctx, floor.ID)
			if err != nil {
				return nil, err
			}
			inventories[floor.ID] = inventory
		}
		var match *plannedAPState
		for _, row := range inventory {
			if row.actual.ID != record.id {
				continue
			}
			want := plannedAPIntent{floor: floor.NameHierarchy, payload: map[string]any{"name": row.actual.Name}}
			if plannedAPResource(want) != key || match != nil {
				return nil, errConfigurationOwned
			}
			match = &plannedAPState{want: want, floorID: floor.ID, floor: floor, actual: row.actual, native: row.native}
		}
		if match == nil {
			return nil, errConfigurationOwned
		}
		if err := e.checkPrivateBaseline(ctx, *match); err != nil {
			return nil, err
		}
		view, err := plannedAPBaselineView(*match)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return json.Marshal(views)
}

// Keep the complete private snapshot in memory only. Reobserve around the
// existing source/owner/journal CAS guard, then let the original sender perform
// its resource-specific preflight. No native response grants replay authority.
func (e *sitePlannedAPExecutor) guardRetainedPrivate(ctx context.Context, before []byte, write *configurationWrite) (*configurationWrite, error) {
	if write == nil || e.placements.comparison == nil || len(e.placements.identities) == 0 {
		return write, nil
	}
	if e.retainedGuard == nil {
		return nil, errConfigurationTransition
	}
	observe := func(ctx context.Context) error {
		sites, err := e.sites.client.ListSites(ctx)
		if err != nil {
			return err
		}
		after, err := e.placements.retainedPrivateView(ctx, sites)
		if err != nil {
			return err
		}
		if !bytes.Equal(before, after) {
			return errConfigurationStale
		}
		return nil
	}
	guardConfigurationPreflight(write, func(ctx context.Context) error {
		if err := observe(ctx); err != nil {
			return err
		}
		if err := e.retainedGuard(ctx, write.resource, write.action, write.identity); err != nil {
			return err
		}
		return observe(ctx)
	})
	return write, nil
}

func siteOrDesignOperation(op *cvk.NetworkControllerConfigurationOperation) bool {
	return siteSingularOperation(op) || siteDesignOperation(op)
}
