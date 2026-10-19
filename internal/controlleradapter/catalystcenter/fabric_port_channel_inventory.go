// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
)

const fabricPortChannelsPath = "/dna/intent/api/v1/sda/portChannels"

// The 2.3.7.6 response does not document native/allowed VLAN readback. Absence
// cannot be interpreted as a default or successful verification of those fields.
type fabricPortChannel struct {
	ID          string   `json:"id"`
	FabricID    string   `json:"fabricId"`
	DeviceID    string   `json:"networkDeviceId"`
	Name        string   `json:"portChannelName"`
	Interfaces  []string `json:"interfaceNames"`
	Type        string   `json:"connectedDeviceType"`
	Protocol    string   `json:"protocol"`
	Description *string  `json:"description"`
}

func (c *client) fabricPortChannels(ctx context.Context, fabricID, deviceID string) ([]fabricPortChannel, error) {
	if !validAPIID(fabricID) || !validAPIID(deviceID) {
		return nil, errInvalidResponse
	}
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		return c.get(ctx, path, q, http.StatusOK)
	}
	rows, err := listInventoryQueryWithGet(ctx, get, fabricPortChannelsPath, url.Values{"fabricId": {fabricID}, "networkDeviceId": {deviceID}}, func(row fabricPortChannel) string { return row.ID })
	if err != nil {
		return nil, err
	}
	names, ports := map[string]bool{}, map[string]bool{}
	for i := range rows {
		row := &rows[i]
		name := strings.ToLower(row.Name)
		if !validAPIID(row.ID) || row.FabricID != fabricID || row.DeviceID != deviceID || !validLANInterfaceName(row.Name) || names[name] || !slices.Contains([]string{"TRUNK", "EXTENDED_NODE"}, row.Type) || !slices.Contains([]string{"ON", "LACP", "PAGP"}, row.Protocol) || len(row.Interfaces) == 0 || len(row.Interfaces) > 16 {
			return nil, errInvalidResponse
		}
		if row.Type == "EXTENDED_NODE" && row.Protocol != "PAGP" || row.Protocol != "LACP" && len(row.Interfaces) > 8 {
			return nil, errInvalidResponse
		}
		names[name] = true
		for _, port := range row.Interfaces {
			key := strings.ToLower(port)
			if !validLANInterfaceName(port) || ports[key] {
				return nil, errInvalidResponse
			}
			ports[key] = true
		}
		sort.Strings(row.Interfaces)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}
