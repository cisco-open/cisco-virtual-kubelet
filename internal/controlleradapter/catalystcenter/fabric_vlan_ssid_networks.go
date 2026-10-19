// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
)

// Resolve both L2 and anycast-backed DATA VLANs with the shared binding rules.
// Every native view of a requested VLAN must explicitly enable wireless.
func resolveFabricVLANSSIDNetworks(fabricID string, mappings []fabricVLANSSID, l2 map[fabricL2Key]fabricL2Network, gateways map[anycastGatewayKey]anycastGateway) ([]fabricPortAssignmentVLANBinding, error) {
	rows, err := canonicalFabricVLANSSIDs(mappings)
	if err != nil {
		return nil, err
	}
	required := map[string]string{}
	for _, row := range rows {
		if len(row.SSIDs) > 0 {
			required[row.VLAN] = "DATA"
		}
	}
	bindings, err := resolveFabricVLANReferences(fabricID, required, l2, gateways)
	if err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		if binding.L2 != nil && (binding.L2.Wireless == nil || !*binding.L2.Wireless) {
			return nil, errConfigurationBusy
		}
		if binding.Gateway != nil {
			var fields map[string]any
			if json.Unmarshal(binding.Gateway, &fields) != nil {
				return nil, errInvalidResponse
			}
			if fields["isWirelessPool"] != true {
				return nil, errConfigurationBusy
			}
		}
	}
	return bindings, nil
}

func (c *client) fabricVLANSSIDNetworks(ctx context.Context, fabricID string, mappings []fabricVLANSSID) ([]fabricPortAssignmentVLANBinding, error) {
	if !validAPIID(fabricID) {
		return nil, errInvalidResponse
	}
	if _, err := canonicalFabricVLANSSIDs(mappings); err != nil {
		return nil, err
	}
	l2, err := c.fabricL2Networks(ctx)
	if err != nil {
		return nil, err
	}
	gateways, err := c.anycastGateways(ctx)
	if err != nil {
		return nil, err
	}
	return resolveFabricVLANSSIDNetworks(fabricID, mappings, l2, gateways)
}
