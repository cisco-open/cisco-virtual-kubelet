// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

type fabricL3IPHandoffDependencies struct {
	binding        fabricL3IPHandoffBinding
	physical       []Device
	before         []fabricL3IPHandoff
	digest         string
	recoveryDigest string
}

// Collect native prerequisites through existing fabric-device and IPAM readers.
// Repeating this snapshot is required before dispatch; it grants no authority
// and does not prove task completion, free pool capacity or device convergence.
func (c *client) fabricL3IPHandoffDependencies(ctx context.Context, want fabricL3IPHandoffIntent, device fabricDeviceIntent) (*fabricL3IPHandoffDependencies, error) {
	if want.device != device.inventory.reference.Name || device.state != "PROVISION" && device.state != "REPROVISION" {
		return nil, errInvalidResponse
	}
	deps, err := c.fabricDeviceDependencies(ctx, device)
	if err != nil {
		return nil, err
	}
	actual, found := deps.before[deps.target.ID]
	if !found || !fabricDeviceConfigurationMatches(deps.native, actual) {
		return nil, errConfigurationBusy
	}
	ports, err := c.lanLinkInterfaces(ctx, deps.target.ID)
	if err != nil {
		return nil, err
	}
	transits, err := c.fabricTransits(ctx)
	if err != nil {
		return nil, err
	}
	networks, err := c.fabricL3Networks(ctx)
	if err != nil {
		return nil, err
	}
	binding, err := bindFabricL3IPHandoff(want, device.inventory.reference.Name, actual, ports, transits, networks)
	if err != nil {
		return nil, err
	}
	poolDigest := ""
	if want.pool != "" {
		sites, err := c.ListSites(ctx)
		if err != nil {
			return nil, err
		}
		path := device.fabricSite
		if device.fabricZone != "" {
			path = device.fabricZone
		}
		owner := ""
		for _, site := range sites {
			if site.NameHierarchy == path {
				owner = site.ID
			}
		}
		if !validAPIID(owner) {
			return nil, errConfigurationStale
		}
		pools, err := c.loadAnycastPoolInventory(ctx)
		if err != nil {
			return nil, err
		}
		// The shared loader validates global allocations, reservation shape and
		// ambiguous names; bindNames ties UUID/prefix/parent evidence to this site.
		pool, err := pools.bindNames([]string{want.pool}, owner)
		if err != nil {
			return nil, err
		}
		poolDigest = pool.digest
	}
	before, err := c.fabricL3IPHandoffs(ctx, actual.FabricID, actual.NetworkDeviceID)
	if err != nil {
		return nil, err
	}
	sort.Slice(before, func(i, j int) bool { return before[i].ID < before[j].ID })
	raw, err := json.Marshal([]any{deps.digest, binding.digest, poolDigest, before})
	if err != nil {
		return nil, errInvalidResponse
	}
	baseline := make([]fabricL3IPHandoff, 0, len(before))
	for _, row := range before {
		if !fabricL3IPHandoffMatches(binding.desired, row) {
			baseline = append(baseline, row)
		}
	}
	recovery, err := json.Marshal([]any{deps.digest, binding.digest, poolDigest, baseline})
	if err != nil {
		return nil, errInvalidResponse
	}
	return &fabricL3IPHandoffDependencies{binding: binding, physical: deps.physical, before: before, digest: fmt.Sprintf("%x", sha256.Sum256(raw)), recoveryDigest: fmt.Sprintf("%x", sha256.Sum256(recovery))}, nil
}
