// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
)

type fabricWirelessMulticastDependencies struct {
	state    fabricWirelessMulticastState
	members  []fabricDeviceMembership
	mappings []fabricVLANSSID
	embedded []fabricSSIDControllerBinding
	digest   string
}

// Retain all fabric memberships, not just the WLC: multicast can affect wired
// fabric nodes too. This is prerequisite evidence, not physical-group authority.
// Fabrics without standalone WLC membership use the embedded controller
// collector and bind its complete native/provisioning evidence.
func (c *client) wirelessMulticastDependencies(ctx context.Context, path string) (*fabricWirelessMulticastDependencies, error) {
	state, err := c.observeFabricWirelessMulticast(ctx, path)
	if err != nil {
		return nil, err
	}
	members, err := c.fabricDeviceMemberships(ctx, state.fabricID)
	if err != nil {
		return nil, err
	}
	out := &fabricWirelessMulticastDependencies{state: state}
	controller := false
	for _, member := range members {
		controller = controller || slices.Contains(member.Roles, "WIRELESS_CONTROLLER_NODE")
		out.members = append(out.members, member)
	}
	if !controller {
		out.embedded, err = c.fabricEmbeddedSSIDControllers(ctx, path, state.fabricID, members, nil, nil)
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out.members, func(i, j int) bool { return out.members[i].NetworkDeviceID < out.members[j].NetworkDeviceID })
	out.mappings, err = c.fabricVLANSSIDs(ctx, state.fabricID)
	if err != nil {
		return nil, err
	}
	ssids := 0
	for _, mapping := range out.mappings {
		ssids += len(mapping.SSIDs)
	}
	if ssids == 0 {
		return nil, errConfigurationBusy
	}
	evidence := []any{state.siteID, state.fabricID, state.enabled, out.members, out.mappings}
	if len(out.embedded) > 0 {
		evidence = append(evidence, out.embedded)
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return nil, err
	}
	out.digest = fmt.Sprintf("%x", sha256.Sum256(raw))
	return out, nil
}
