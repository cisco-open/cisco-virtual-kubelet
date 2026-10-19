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

type fabricMulticastSnapshot struct {
	siteID, fabricID, digest, dependencyDigest string
	nameIDs                                    map[string]string
	networkIDs                                 map[string]string
	bodies                                     map[string]map[string]any
	before                                     map[string]fabricMulticastVN
	devices                                    []Device
}

// Snapshot scope is one preflight pass. Its digest binds the pre-submit state;
// it is not a completion receipt, and must not be reused across reconciliations.
func (c *client) multicastSnapshot(ctx context.Context, path string, wants []fabricMulticastVNIntent) (*fabricMulticastSnapshot, error) {
	if !validSitePath(path) || path == "Global" || len(wants) == 0 || len(wants) > 1000 {
		return nil, errInvalidResponse
	}
	refs := []fabricL3AssociationIntent{}
	seen := map[string]bool{}
	for _, want := range wants {
		if !validProfileName(want.name) || seen[want.name] {
			return nil, errInvalidResponse
		}
		seen[want.name] = true
		refs = append(refs, fabricL3AssociationIntent{path: path, name: want.name})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].name < refs[j].name })
	states, err := (&fabricL3AssociationExecutor{client: c, desired: refs}).observe(ctx)
	if err != nil {
		return nil, err
	}
	out := &fabricMulticastSnapshot{networkIDs: map[string]string{}, bodies: map[string]map[string]any{}, before: map[string]fabricMulticastVN{}}
	networks := []fabricL3Network{}
	for _, state := range states {
		if !state.ready || !state.exists {
			return nil, errConfigurationBusy
		}
		if out.fabricID != "" && (out.fabricID != state.fabricID || out.siteID != state.siteID) {
			return nil, errConfigurationStale
		}
		out.fabricID = state.fabricID
		out.siteID = state.siteID
		network := state.network
		out.networkIDs[network.Name] = network.ID
		network.Fabrics = append([]string{}, network.Fabrics...)
		sort.Strings(network.Fabrics)
		networks = append(networks, network)
	}
	native, err := c.fabricMulticastVNs(ctx)
	if err != nil {
		return nil, err
	}
	pools, err := c.loadAnycastPoolInventory(ctx)
	if err != nil {
		return nil, err
	}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	names, err := resolveMulticastRPNames(wants, inventory)
	if err != nil {
		return nil, err
	}
	out.nameIDs = names
	memberships, err := c.fabricDeviceMemberships(ctx, out.fabricID)
	if err != nil {
		return nil, err
	}
	proposed := []fabricMulticastVN{}
	for _, want := range wants {
		var prior *fabricMulticastVN
		if row, exists := native[fabricMulticastVNKey{out.fabricID, want.name}]; exists {
			prior = &row
			out.before[want.name] = row
		}
		body, err := fabricMulticastPayload(want, out.fabricID, names, prior)
		if err != nil {
			return nil, err
		}
		out.bodies[want.name] = body
		candidate := map[string]any{}
		for k, v := range body {
			candidate[k] = v
		}
		if prior == nil {
			candidate["id"] = fmt.Sprintf("planned-%x", sha256.Sum256([]byte(want.name)))
			// Omitted RP intent for a new VN means no proposed fabric RPs. This is
			// internal preflight evidence only, never fabricated native readback.
			if _, present := candidate["multicastRPs"]; !present {
				candidate["multicastRPs"] = []map[string]any{}
			}
		}
		raw, err := json.Marshal(candidate)
		if err != nil {
			return nil, err
		}
		row, err := parseFabricMulticastVNObservation(raw)
		if err != nil {
			return nil, err
		}
		proposed = append(proposed, row)
	}
	evidence, err := validateFabricMulticastSite(out.fabricID, out.siteID, native, proposed, pools, memberships)
	if err != nil {
		return nil, err
	}
	// The API deploys fabric-wide configuration. RP eligibility is a subset;
	// mutation coordination must fence every member, including non-RP devices.
	allMembers, physicalTargets, err := bindFabricMutationTargets(out.fabricID, memberships, inventory)
	if err != nil {
		return nil, err
	}
	out.devices = physicalTargets
	physical := map[string]string{}
	for _, device := range out.devices {
		physical[device.ID] = device.Serial
	}
	observed := map[string]map[string]any{}
	for key, row := range native {
		if key.fabricID == out.fabricID {
			observed[key.network] = row.fields
		}
	}
	raw, err := json.Marshal(struct {
		SiteID, FabricID   string
		Networks           []fabricL3Network
		Observed, Proposed map[string]map[string]any
		Members            []fabricDeviceMembership
		Pools, Physical    map[string]string
	}{out.siteID, out.fabricID, networks, observed, out.bodies, allMembers, evidence.poolDigests, physical})
	if err != nil {
		return nil, err
	}
	out.digest = fmt.Sprintf("%x", sha256.Sum256(raw))
	retained := map[string]map[string]any{}
	for name, fields := range observed {
		if !seen[name] {
			retained[name] = fields
		}
	}
	raw, err = json.Marshal(struct {
		SiteID, FabricID string
		Networks         []fabricL3Network
		Members          []fabricDeviceMembership
		Pools, Physical  map[string]string
		Retained         map[string]map[string]any
	}{out.siteID, out.fabricID, networks, allMembers, evidence.poolDigests, physical, retained})
	if err != nil {
		return nil, err
	}
	out.dependencyDigest = fmt.Sprintf("%x", sha256.Sum256(raw))
	return out, nil
}
