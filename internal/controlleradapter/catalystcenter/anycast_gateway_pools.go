// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

type anycastPoolDependencies struct {
	primary   ipReservation
	secondary []ipReservation
	digest    string
}

type anycastPoolInventory struct {
	byName map[string]ipReservation
	byID   map[string]ipPool
}

func (c *client) loadAnycastPoolInventory(ctx context.Context) (*anycastPoolInventory, error) {
	pools, err := c.listIPPools(ctx)
	if err != nil {
		return nil, err
	}
	reservations, err := c.listIPReservations(ctx)
	if err != nil {
		return nil, err
	}
	if err = validateIPAMAllocation(networkReportIntent{}, pools, reservations); err != nil {
		return nil, err
	}
	out := &anycastPoolInventory{byName: map[string]ipReservation{}, byID: map[string]ipPool{}}
	for _, r := range reservations {
		out.byName[r.Name] = r
	}
	for _, p := range pools {
		out.byID[p.ID] = p
	}
	return out, nil
}
func (c *client) anycastPoolDependencies(ctx context.Context, want anycastGatewayIntent, reservationSiteID string) (anycastPoolDependencies, error) {
	if !validAPIID(reservationSiteID) || !validPoolName(want.pool) {
		return anycastPoolDependencies{}, errInvalidResponse
	}
	inventory, err := c.loadAnycastPoolInventory(ctx)
	if err != nil {
		return anycastPoolDependencies{}, err
	}
	return inventory.bind(want, reservationSiteID)
}

// The reservation owner comes from topology, not a name-based inheritance guess.
func (inventory *anycastPoolInventory) bind(want anycastGatewayIntent, reservationSiteID string) (anycastPoolDependencies, error) {
	if inventory == nil || !validAPIID(reservationSiteID) || !validPoolName(want.pool) {
		return anycastPoolDependencies{}, errInvalidResponse
	}
	names, err := anycastPoolNames(want)
	if err != nil {
		return anycastPoolDependencies{}, err
	}
	return inventory.bindNames(names, reservationSiteID)
}

func anycastPoolNames(want anycastGatewayIntent) ([]string, error) {
	if !validPoolName(want.pool) {
		return nil, errInvalidResponse
	}
	names := []string{want.pool}
	if value, exists := want.payload["additionalIpPools"]; exists {
		secondary, ok := value.([]map[string]any)
		if !ok || len(secondary) > 4 {
			return nil, errInvalidResponse
		}
		seen := map[string]bool{want.pool: true}
		for i, p := range secondary {
			name, ok := p["name"].(string)
			if !ok || !validPoolName(name) || seen[name] || p["order"] != i+2 {
				return nil, errInvalidResponse
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	return names, nil
}

// Shared reservation evidence for fabric consumers; callers supply ordered names.
func (inventory *anycastPoolInventory) bindNames(names []string, reservationSiteID string) (anycastPoolDependencies, error) {
	if inventory == nil || !validAPIID(reservationSiteID) || len(names) == 0 {
		return anycastPoolDependencies{}, errInvalidResponse
	}
	seen := map[string]bool{}
	for _, name := range names {
		if !validPoolName(name) || seen[name] {
			return anycastPoolDependencies{}, errInvalidResponse
		}
		seen[name] = true
	}
	selected := []ipReservation{}
	parents := map[string]ipPool{}
	for _, name := range names {
		r, exists := inventory.byName[name]
		if !exists {
			return anycastPoolDependencies{}, errConfigurationBusy
		}
		if !validAPIID(r.ID) || r.SiteID != reservationSiteID {
			return anycastPoolDependencies{}, errConfigurationStale
		}
		selected = append(selected, r)
		for _, space := range []*poolAddressSpace{r.V4, r.V6} {
			if space != nil {
				parents[space.GlobalPoolID] = inventory.byID[space.GlobalPoolID]
			}
		}
	}
	globals := []ipPool{}
	for _, p := range parents {
		globals = append(globals, p)
	}
	sort.Slice(globals, func(i, j int) bool { return globals[i].ID < globals[j].ID })
	evidence := struct {
		SiteID       string
		Reservations []ipReservation
		Parents      []ipPool
	}{reservationSiteID, selected, globals}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return anycastPoolDependencies{}, err
	}
	return anycastPoolDependencies{primary: selected[0], secondary: selected[1:], digest: fmt.Sprintf("%x", sha256.Sum256(raw))}, nil
}
