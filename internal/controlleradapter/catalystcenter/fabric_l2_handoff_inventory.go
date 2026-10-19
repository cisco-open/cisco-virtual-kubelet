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

const fabricL2HandoffsPath = "/dna/intent/api/v1/sda/fabricDevices/layer2Handoffs"

type fabricL2Handoff struct {
	ID           string `json:"id,omitempty"`
	DeviceID     string `json:"networkDeviceId"`
	FabricID     string `json:"fabricId"`
	Port         string `json:"interfaceName"`
	InternalVLAN int    `json:"internalVlanId"`
	ExternalVLAN int    `json:"externalVlanId"`
}

func validFabricL2Handoff(row fabricL2Handoff) bool {
	return validAPIID(row.DeviceID) && validAPIID(row.FabricID) && validLANInterfaceName(row.Port) && validFabricVLAN(row.InternalVLAN) && row.ExternalVLAN >= 1 && row.ExternalVLAN <= 4094
}

// Scoped native inventory reuses the shared authenticated transport and bounded
// pagination. Duplicate native IDs and conflicting interface VLAN mappings are
// rejected before any comparison or write planning can use the snapshot.
func (c *client) fabricL2Handoffs(ctx context.Context, fabricID, deviceID string) ([]fabricL2Handoff, error) {
	if !validAPIID(fabricID) || !validAPIID(deviceID) {
		return nil, errInvalidResponse
	}
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		return c.get(ctx, path, q, http.StatusOK)
	}
	rows, err := listInventoryQueryWithGet(ctx, get, fabricL2HandoffsPath, url.Values{"fabricId": {fabricID}, "networkDeviceId": {deviceID}}, func(r fabricL2Handoff) string { return r.ID })
	if err != nil {
		return nil, err
	}
	internal, external := map[string]bool{}, map[string]bool{}
	for _, row := range rows {
		if !validAPIID(row.ID) || !validFabricL2Handoff(row) || row.FabricID != fabricID || row.DeviceID != deviceID {
			return nil, errInvalidResponse
		}
		port := strings.ToLower(row.Port) + "\x00"
		ik, ek := port+strconv.Itoa(row.InternalVLAN), port+strconv.Itoa(row.ExternalVLAN)
		if internal[ik] || external[ek] {
			return nil, errInvalidResponse
		}
		internal[ik], external[ek] = true, true
	}
	return rows, nil
}
