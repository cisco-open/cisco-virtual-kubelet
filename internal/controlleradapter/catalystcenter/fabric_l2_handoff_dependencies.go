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

type fabricL2HandoffDependencies struct {
	binding        fabricL2HandoffBinding
	physical       []Device
	before         []fabricL2Handoff
	digest         string
	recoveryDigest string
}

// Collect one native dependency snapshot using existing inventory, interface,
// fabric and VLAN readers. The declared site is resolved to its native fabric
// on every observation. Callers must repeat this before dispatch; it grants no authority.
func (c *client) fabricL2HandoffDependencies(ctx context.Context, want fabricL2HandoffIntent, ref configurationDeviceReference, sitePath string) (*fabricL2HandoffDependencies, error) {
	if ref.Name != want.device || !validSitePath(sitePath) || sitePath == "Global" {
		return nil, errInvalidResponse
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	var site Site
	for _, candidate := range sites {
		if candidate.NameHierarchy == sitePath {
			site = candidate
		}
	}
	if !validAPIID(site.ID) {
		return nil, errConfigurationBusy
	}
	fabrics, err := c.fabricSites(ctx)
	if err != nil {
		return nil, err
	}
	fabric, exists := fabrics[site.ID]
	if !exists {
		return nil, errConfigurationBusy
	}
	fabricID := fabric.ID
	scope := []any{site.ID, site.NameHierarchy, fabric}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	target, err := resolveConfigurationDevice(ref, inventory)
	if err != nil {
		return nil, err
	}
	members, err := c.fabricDeviceConfigurations(ctx, fabricID)
	if err != nil {
		return nil, err
	}
	device, exists := members[target.ID]
	if !exists {
		return nil, errConfigurationBusy
	}
	ids := make([]string, 0, len(members))
	for id := range members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	physical, err := bindMulticastRPDevices(ids, inventory)
	if err != nil {
		return nil, err
	}
	ports, err := c.lanLinkInterfaces(ctx, target.ID)
	if err != nil {
		return nil, err
	}
	gateways, err := c.anycastGateways(ctx)
	if err != nil {
		return nil, err
	}
	networks, err := c.fabricL2Networks(ctx)
	if err != nil {
		return nil, err
	}
	gw := make([]anycastGateway, 0, len(gateways))
	for _, row := range gateways {
		gw = append(gw, row)
	}
	l2 := make([]fabricL2Network, 0, len(networks))
	for _, row := range networks {
		l2 = append(l2, row)
	}
	binding, err := bindFabricL2Handoff(want, ref.Name, device, ports, gw, l2)
	if err != nil {
		return nil, err
	}
	before, err := c.fabricL2Handoffs(ctx, fabricID, target.ID)
	if err != nil {
		return nil, err
	}
	sort.Slice(before, func(i, j int) bool { return before[i].ID < before[j].ID })
	raw, err := json.Marshal([]any{scope, binding.digest, physical, members, before})
	if err != nil {
		return nil, errInvalidResponse
	}
	baseline := make([]fabricL2Handoff, 0, len(before))
	for _, row := range before {
		if !fabricL2HandoffMatches(binding.desired, row) {
			baseline = append(baseline, row)
		}
	}
	recovery, err := json.Marshal([]any{scope, binding.digest, physical, members, baseline})
	if err != nil {
		return nil, errInvalidResponse
	}
	return &fabricL2HandoffDependencies{binding: binding, physical: physical, before: before, digest: fmt.Sprintf("%x", sha256.Sum256(raw)), recoveryDigest: fmt.Sprintf("%x", sha256.Sum256(recovery))}, nil
}
