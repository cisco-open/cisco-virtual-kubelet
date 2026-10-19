// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const fabricL3IPHandoffsPath = "/dna/intent/api/v1/sda/fabricDevices/layer3Handoffs/ipTransits"

type fabricL3IPHandoff struct {
	ID        string `json:"id,omitempty"`
	DeviceID  string `json:"networkDeviceId"`
	FabricID  string `json:"fabricId"`
	TransitID string `json:"transitNetworkId"`
	Port      string `json:"interfaceName"`
	Network   string `json:"virtualNetworkName"`
	VLAN      int    `json:"vlanId"`
	Pool      string `json:"externalConnectivityIpPoolName,omitempty"`
	MSS       *int   `json:"tcpMssAdjustment,omitempty"`
	Local4    string `json:"localIpAddress,omitempty"`
	Peer4     string `json:"remoteIpAddress,omitempty"`
	Local6    string `json:"localIpv6Address,omitempty"`
	Peer6     string `json:"remoteIpv6Address,omitempty"`
}

func validFabricL3IPHandoff(row fabricL3IPHandoff) bool {
	return validAPIID(row.DeviceID) && validAPIID(row.FabricID) && validAPIID(row.TransitID) && validLANInterfaceName(row.Port) && validProfileName(row.Network) && validFabricVLAN(row.VLAN) && (row.Pool == "" || validProfileName(row.Pool)) && (row.MSS == nil || *row.MSS >= 500 && *row.MSS <= 1440) && validFabricHandoffPrefix(row.Local4, true) && validFabricHandoffPrefix(row.Peer4, true) && validFabricHandoffPrefix(row.Local6, false) && validFabricHandoffPrefix(row.Peer6, false)
}

// Use the existing authenticated, strict-200 paginated reader. Scope and mapping
// checks apply to the complete result; errors never return a partial inventory.
func (c *client) fabricL3IPHandoffs(ctx context.Context, fabricID, deviceID string) ([]fabricL3IPHandoff, error) {
	if !validAPIID(fabricID) || !validAPIID(deviceID) {
		return nil, errInvalidResponse
	}
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		return c.get(ctx, path, q, http.StatusOK)
	}
	rows, err := listInventoryQueryWithGet(ctx, get, fabricL3IPHandoffsPath, url.Values{"fabricId": {fabricID}, "networkDeviceId": {deviceID}}, func(row fabricL3IPHandoff) string { return row.ID })
	if err != nil {
		return nil, err
	}
	mappings, tags := map[string]bool{}, map[string]bool{}
	for _, row := range rows {
		if !validAPIID(row.ID) || !validFabricL3IPHandoff(row) || row.FabricID != fabricID || row.DeviceID != deviceID {
			return nil, errInvalidResponse
		}
		port := strings.ToLower(row.Port) + "\x00"
		mapping := port + row.TransitID + "\x00" + row.Network
		tag := port + strconv.Itoa(row.VLAN)
		if mappings[mapping] || tags[tag] {
			return nil, errInvalidResponse
		}
		mappings[mapping], tags[tag] = true, true
	}
	return rows, nil
}
