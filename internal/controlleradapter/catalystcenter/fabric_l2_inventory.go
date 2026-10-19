// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"net/url"
)

const fabricL2NetworksPath = "/dna/intent/api/v1/sda/layer2VirtualNetworks"

type fabricL2Network struct {
	ID       string `json:"id,omitempty"`
	FabricID string `json:"fabricId"`
	VLANName string `json:"vlanName"`
	VLAN     int    `json:"vlanId,omitempty"`
	Traffic  string `json:"trafficType"`
	Wireless *bool  `json:"isFabricEnabledWireless,omitempty"`
	L3       string `json:"associatedLayer3VirtualNetworkName,omitempty"`
}
type fabricL2Key struct{ fabricID, vlanName string }

func (c *client) fabricL2Networks(ctx context.Context) (map[fabricL2Key]fabricL2Network, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	rows, err := listInventoryPagesWithGet(ctx, get, fabricL2NetworksPath, nil, func(n fabricL2Network) string { return n.ID }, 50)
	if err != nil {
		return nil, err
	}
	out := map[fabricL2Key]fabricL2Network{}
	ids := map[string]map[int]bool{}
	for _, n := range rows {
		if !validAPIID(n.ID) || !validAPIID(n.FabricID) || !validFabricVLANName(n.VLANName) || !validFabricVLAN(n.VLAN) || (n.Traffic != "DATA" && n.Traffic != "VOICE") || n.Wireless == nil || n.L3 != "" && !validProfileName(n.L3) {
			return nil, errInvalidResponse
		}
		key := fabricL2Key{n.FabricID, n.VLANName}
		if _, exists := out[key]; exists {
			return nil, errInvalidResponse
		}
		if ids[n.FabricID] == nil {
			ids[n.FabricID] = map[int]bool{}
		}
		if ids[n.FabricID][n.VLAN] {
			return nil, errInvalidResponse
		}
		ids[n.FabricID][n.VLAN] = true
		out[key] = n
	}
	return out, nil
}
