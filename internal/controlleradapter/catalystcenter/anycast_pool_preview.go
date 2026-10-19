// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "context"

// A preview admits declared prerequisites for planning only. It intentionally
// contains no reservation UUIDs or dependency digest and cannot replace native
// anycastPoolDependencies at submission or verification.
type anycastPoolPreview struct{ pending bool }

// One read-only preview snapshot is shared across all gateway references in a
// preflight pass. Native submission still recollects authoritative dependencies.
type anycastPoolPreviewInventory struct {
	byName, byID, wantedPools map[string]ipPool
	actual                    map[string]ipReservation
	desired                   map[string]desiredReservation
	sites                     map[string]Site
}

func (e *ipamConfigurationExecutor) loadAnycastPoolPreview(ctx context.Context) (*anycastPoolPreviewInventory, error) {
	if e == nil || e.client == nil {
		return nil, errInvalidResponse
	}
	pools, reservations, sites, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]ipPool{}
	byID := map[string]ipPool{}
	for _, pool := range pools {
		byName[pool.Name] = pool
		byID[pool.ID] = pool
	}
	wantedPools := map[string]ipPool{}
	for _, pool := range e.intent.pools {
		wantedPools[pool.Name] = pool
	}
	actual := map[string]ipReservation{}
	for _, reservation := range reservations {
		actual[reservation.Name] = reservation
	}
	desired := map[string]desiredReservation{}
	for _, reservation := range e.intent.reservations {
		desired[reservation.Name] = reservation
	}
	return &anycastPoolPreviewInventory{byName: byName, byID: byID, wantedPools: wantedPools, actual: actual, desired: desired, sites: sites}, nil
}
func (e *ipamConfigurationExecutor) previewAnycastPools(ctx context.Context, want anycastGatewayIntent, ownerID string) (anycastPoolPreview, error) {
	inventory, err := e.loadAnycastPoolPreview(ctx)
	if err != nil {
		return anycastPoolPreview{}, err
	}
	return inventory.preview(want, ownerID)
}
func (inventory *anycastPoolPreviewInventory) preview(want anycastGatewayIntent, ownerID string) (anycastPoolPreview, error) {
	zero := anycastPoolPreview{}
	if inventory == nil || !validAPIID(ownerID) {
		return zero, errInvalidResponse
	}
	names, err := anycastPoolNames(want)
	if err != nil {
		return zero, err
	}
	byName, byID, wantedPools, actual, desired, sites := inventory.byName, inventory.byID, inventory.wantedPools, inventory.actual, inventory.desired, inventory.sites
	out := anycastPoolPreview{}
	for _, name := range names {
		old, exists := actual[name]
		declaration, declared := desired[name]
		if exists && old.SiteID != ownerID {
			return zero, errConfigurationStale
		}
		if !declared {
			if !exists {
				return zero, errConfigurationBusy
			}
			for _, space := range []*poolAddressSpace{old.V4, old.V6} {
				if space == nil {
					continue
				}
				parent := byID[space.GlobalPoolID]
				if declaration, found := wantedPools[parent.Name]; found {
					diff, err := poolSpaceDiff(declaration.Space, parent.Space)
					if err != nil {
						return zero, err
					}
					if len(diff) > 0 {
						out.pending = true
					}
				}
			}
			continue
		}
		if sites[declaration.site].ID != ownerID {
			return zero, errConfigurationStale
		}
		if !exists {
			out.pending = true
		}
		for i, space := range []*poolAddressSpace{declaration.V4, declaration.V6} {
			if space == nil {
				continue
			}
			observed := old.V4
			if i == 1 {
				observed = old.V6
			}
			diff, err := poolSpaceDiff(space, observed)
			if err != nil {
				return zero, err
			}
			if len(diff) > 0 {
				out.pending = true
			}
			parent, present := byName[space.GlobalPoolID]
			parentDeclaration, parentDeclared := wantedPools[space.GlobalPoolID]
			if !present {
				if !parentDeclared {
					return zero, errConfigurationBusy
				}
				out.pending = true
				continue
			}
			if observed != nil && observed.GlobalPoolID != parent.ID {
				return zero, errConfigurationStale
			}
			if parentDeclared {
				diff, err := poolSpaceDiff(parentDeclaration.Space, parent.Space)
				if err != nil {
					return zero, err
				}
				if len(diff) > 0 {
					out.pending = true
				}
			}
		}
	}
	return out, nil
}
