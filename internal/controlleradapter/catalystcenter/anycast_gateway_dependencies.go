// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

type anycastGatewayDependencies struct {
	siteID, fabricID, networkID, reservationSiteID, release, digest string
	pools                                                           anycastPoolDependencies
	parent                                                          *anycastGateway
	actual                                                          anycastGateway
	exists                                                          bool
	pendingParent                                                   bool
}

// Snapshot lifetime is one preflight/readback pass. It is never reused across
// submission or reconciliation boundaries.
type anycastGatewayInventory struct {
	states    map[fabricL3AssociationIntent]fabricL3AssociationState
	fabrics   map[string]fabricSite
	pools     *anycastPoolInventory
	gateways  map[anycastGatewayKey]anycastGateway
	layer2    map[fabricL2Key]fabricL2Network
	release   string
	hierarchy map[string]Site
}

func (c *client) loadAnycastGatewayInventory(ctx context.Context, desired []fabricAnycastIntent) (*anycastGatewayInventory, error) {
	if len(desired) == 0 || len(desired) > 1000 {
		return nil, errInvalidResponse
	}
	refs := []fabricL3AssociationIntent{}
	seen := map[fabricL3AssociationIntent]bool{}
	for _, w := range desired {
		ref := fabricL3AssociationIntent{path: w.path, parent: w.parent, name: w.network}
		if !seen[ref] {
			refs = append(refs, ref)
			seen[ref] = true
		}
	}
	states, err := (&fabricL3AssociationExecutor{client: c, desired: refs}).observe(ctx)
	if err != nil {
		return nil, err
	}
	out := &anycastGatewayInventory{states: map[fabricL3AssociationIntent]fabricL3AssociationState{}, fabrics: map[string]fabricSite{}, hierarchy: map[string]Site{}}
	for _, state := range states {
		out.states[state.want] = state
	}
	fabrics, err := c.fabricSites(ctx)
	if err != nil {
		return nil, err
	}
	hierarchy, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, s := range hierarchy {
		known[s.ID] = true
		out.hierarchy[s.NameHierarchy] = s
	}
	for _, f := range fabrics {
		if !known[f.SiteID] {
			return nil, errInvalidResponse
		}
		out.fabrics[f.ID] = f
	}
	for _, w := range desired {
		if w.needsSecondaryPools {
			out.release, err = c.anycastCapabilityEvidence(ctx, w.anycastGatewayIntent)
			if err != nil {
				return nil, err
			}
			break
		}
	}
	out.pools, err = c.loadAnycastPoolInventory(ctx)
	if err != nil {
		return nil, err
	}
	out.gateways, err = c.anycastGateways(ctx)
	if err != nil {
		return nil, err
	}
	out.layer2, err = c.fabricL2Networks(ctx)
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (c *client) anycastGatewayDependencies(ctx context.Context, want fabricAnycastIntent) (anycastGatewayDependencies, error) {
	inventory, err := c.loadAnycastGatewayInventory(ctx, []fabricAnycastIntent{want})
	if err != nil {
		return anycastGatewayDependencies{}, err
	}
	return inventory.resolve(want)
}
func (inventory *anycastGatewayInventory) resolve(want fabricAnycastIntent) (anycastGatewayDependencies, error) {
	return inventory.resolveWithPending(want, nil)
}
func (inventory *anycastGatewayInventory) resolveWithPending(want fabricAnycastIntent, pending map[anycastGatewayKey]bool) (anycastGatewayDependencies, error) {
	zero := anycastGatewayDependencies{}
	if inventory == nil {
		return zero, errInvalidResponse
	}
	ref := fabricL3AssociationIntent{path: want.path, parent: want.parent, name: want.network}
	state, ok := inventory.states[ref]
	if !ok || !state.ready || !state.exists {
		return zero, errConfigurationBusy
	}
	out := anycastGatewayDependencies{siteID: state.siteID, fabricID: state.fabricID, networkID: state.network.ID, reservationSiteID: state.siteID}
	parentFabricID := ""
	if want.parent != "" {
		parent, ok := inventory.fabrics[state.parentFabricID]
		if !ok {
			return zero, errConfigurationStale
		}
		out.reservationSiteID = parent.SiteID
		parentFabricID = parent.ID
	}
	if state.network.Anchor != "" {
		anchor, ok := inventory.fabrics[state.network.Anchor]
		if !ok {
			return zero, errConfigurationStale
		}
		out.reservationSiteID = anchor.SiteID
		if want.parent == "" && anchor.ID != state.fabricID {
			parentFabricID = anchor.ID
		}
	}
	if want.needsSecondaryPools {
		if !supportsAnycastSecondaryPools(inventory.release) {
			return zero, errConfigurationStale
		}
		out.release = inventory.release
	}
	var err error
	out.pools, err = inventory.pools.bind(want.anycastGatewayIntent, out.reservationSiteID)
	if err != nil {
		return zero, err
	}
	if parentFabricID != "" {
		parent, exists := inventory.gateways[anycastGatewayKey{parentFabricID, want.pool}]
		if !exists {
			if !pending[anycastGatewayKey{parentFabricID, want.pool}] {
				return zero, errConfigurationBusy
			}
			out.pendingParent = true
		} else {
			matches, err := anycastGatewayMatches(want.anycastGatewayIntent, parent, out.pools, nil)
			if err != nil {
				return zero, err
			}
			if !matches {
				return zero, errConfigurationTransition
			}
			out.parent = &parent
		}
	}
	out.actual, out.exists = inventory.gateways[anycastGatewayKey{out.fabricID, want.pool}]
	if out.exists && out.pendingParent {
		return zero, errConfigurationStale
	}
	if !out.exists {
		if err := inventory.checkGatewayVLANConflict(want, out); err != nil {
			return zero, err
		}
	}
	var parentFields map[string]any
	if out.parent != nil {
		parentFields = out.parent.fields
	}
	evidence := struct {
		Path, ParentPath, SiteID, FabricID, NetworkID, AnchorID, ReservationSiteID, PoolDigest, Release string
		ParentFields                                                                                    map[string]any
	}{want.path, want.parent, out.siteID, out.fabricID, out.networkID, state.network.Anchor, out.reservationSiteID, out.pools.digest, out.release, parentFields}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return zero, err
	}
	out.digest = fmt.Sprintf("%x", sha256.Sum256(raw))
	return out, nil
}

// Check allocations we can determine before the controller assigns a VLAN.
// Scope is the target fabric: parent/anchor gateways legitimately share a VLAN.
func resolvedAnycastVLANAllocation(want fabricAnycastIntent, deps anycastGatewayDependencies) (int, string, error) {
	id, _ := want.payload["vlanId"].(int)
	name, _ := want.payload["vlanName"].(string)
	if deps.parent != nil {
		id, name = deps.parent.VLAN, deps.parent.VLANName
	} else if want.payload["autoGenerateVlanName"] == true {
		critical, known := want.payload["isCriticalPool"].(bool)
		if want.network == "INFRA_VN" {
			critical, known = false, true
		}
		if known {
			var err error
			name, err = generatedAnycastVLANName(want.network, critical, want.payload["trafficType"], deps.pools)
			if err != nil {
				return 0, "", err
			}
		}
	}
	return id, name, nil
}

func (inventory *anycastGatewayInventory) checkGatewayVLANConflict(want fabricAnycastIntent, deps anycastGatewayDependencies) error {
	id, name, err := resolvedAnycastVLANAllocation(want, deps)
	if err != nil {
		return err
	}

	for _, other := range inventory.gateways {
		if other.FabricID != deps.fabricID || other.Pool == want.pool {
			continue
		}
		if id != 0 && id == other.VLAN || name != "" && name == other.VLANName {
			return errConfigurationTransition
		}
	}
	// Native Layer-2 virtual networks share the target fabric's VLAN space.
	// Matching an L3 association does not establish ownership of an existing L2
	// allocation; do not reuse it for a new gateway implicitly. This check runs
	// again immediately before submission through dependency resolution.
	for _, other := range inventory.layer2 {
		if other.FabricID == deps.fabricID && (id != 0 && id == other.VLAN || name != "" && name == other.VLANName) {
			return errConfigurationTransition
		}
	}
	return nil
}
