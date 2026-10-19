// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"net/http"
	"net/url"
)

const fabricSDAHandoffsPath = "/dna/intent/api/v1/sda/fabricDevices/layer3Handoffs/sdaTransits"

// SDA handoffs have a device/fabric identity, not the UUID used by IP handoffs.
type fabricSDAHandoff struct {
	DeviceID  string `json:"networkDeviceId"`
	FabricID  string `json:"fabricId"`
	TransitID string `json:"transitNetworkId"`
	Prime     *int64 `json:"affinityIdPrime,omitempty"`
	Decider   *int64 `json:"affinityIdDecider,omitempty"`
	Internet  *bool  `json:"connectedToInternet,omitempty"`
	Multicast *bool  `json:"isMulticastOverTransitEnabled,omitempty"`
}

func validFabricSDAAffinity(value *int64) bool {
	return value == nil || *value >= 0 && *value <= 2147483647
}

func (c *client) fabricSDAHandoffs(ctx context.Context, fabricID, deviceID string) ([]fabricSDAHandoff, error) {
	if !validAPIID(fabricID) || deviceID != "" && !validAPIID(deviceID) {
		return nil, errInvalidResponse
	}
	query := url.Values{"fabricId": {fabricID}}
	if deviceID != "" {
		query.Set("networkDeviceId", deviceID)
	}
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		return c.get(ctx, path, q, http.StatusOK)
	}
	rows, err := listInventoryQueryWithGet(ctx, get, fabricSDAHandoffsPath, query, func(row fabricSDAHandoff) string { return row.FabricID + "\x00" + row.DeviceID })
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if !validAPIID(row.DeviceID) || !validAPIID(row.FabricID) || !validAPIID(row.TransitID) || row.FabricID != fabricID || deviceID != "" && row.DeviceID != deviceID || !validFabricSDAAffinity(row.Prime) || !validFabricSDAAffinity(row.Decider) {
			return nil, errInvalidResponse
		}
	}
	return rows, nil
}
