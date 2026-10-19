// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
)

// Resolve the requested data VLAN's VN from existing native evidence. An L2
// network without an L3 association cannot establish scalable-group membership.
func fabricPortAssignmentNetwork(binding fabricPortAssignmentVLANBinding) (string, error) {
	network := ""
	if binding.L2 != nil {
		network = binding.L2.L3
	}
	if len(binding.Gateway) > 0 {
		var gateway struct {
			Network string `json:"virtualNetworkName"`
		}
		if json.Unmarshal(binding.Gateway, &gateway) != nil || !validProfileName(gateway.Network) {
			return "", errInvalidResponse
		}
		if network != "" && network != gateway.Network {
			return "", errConfigurationStale
		}
		network = gateway.Network
	}
	if !validProfileName(network) {
		return "", errConfigurationTransition
	}
	return network, nil
}

// Observes membership only; it neither owns nor modifies ISE security groups.
// Explicit data-VLAN intent is currently required when a group is requested.
// Inherited/default VLAN selection needs separate native qualification.
func (c *client) fabricPortAssignmentGroups(ctx context.Context, wants []fabricPortAssignmentIntent, vlans []fabricPortAssignmentVLANBinding) ([]fabricScalableGroupMembership, error) {
	rows, err := expandFabricPortAssignments(wants)
	if err != nil || len(rows) == 0 {
		return nil, errInvalidResponse
	}
	byName := map[string]fabricPortAssignmentVLANBinding{}
	for _, binding := range vlans {
		if _, duplicate := byName[binding.Name]; duplicate {
			return nil, errInvalidResponse
		}
		byName[binding.Name] = binding
	}
	required := map[string]map[string]bool{}
	for _, row := range rows {
		if row.SecurityGroup == nil {
			continue
		}
		if row.DataVLAN == nil {
			return nil, errConfigurationTransition
		}
		binding, present := byName[*row.DataVLAN]
		if !present || binding.Traffic != "DATA" {
			return nil, errConfigurationBusy
		}
		network, err := fabricPortAssignmentNetwork(binding)
		if err != nil {
			return nil, err
		}
		if required[network] == nil {
			required[network] = map[string]bool{}
		}
		required[network][*row.SecurityGroup] = true
	}
	networks := make([]string, 0, len(required))
	for name := range required {
		networks = append(networks, name)
	}
	sort.Strings(networks)
	out := []fabricScalableGroupMembership{}
	for _, name := range networks {
		membership, err := c.fabricScalableGroups(ctx, name)
		if err != nil {
			return nil, err
		}
		for group := range required[name] {
			if !slices.Contains(membership.Groups, group) {
				return nil, errConfigurationBusy
			}
		}
		out = append(out, *membership)
	}
	return out, nil
}
