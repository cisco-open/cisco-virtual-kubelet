// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode"
)

// Keep identity and configuration evidence, excluding traffic counters and
// collection timestamps which change independently of configuration.
type lanLinkInterface struct {
	ID          string `json:"id"`
	DeviceID    string `json:"deviceId"`
	PortName    string `json:"portName"`
	Type        string `json:"interfaceType"`
	Index       string `json:"ifIndex"`
	AdminStatus string `json:"adminStatus"`
	Status      string `json:"status"`
	Mode        string `json:"portMode"`
	IPv4        string `json:"ipv4Address"`
	Mask        string `json:"ipv4Mask"`
	ISIS        string `json:"isisSupport"`
	VLAN        string `json:"vlanId"`
	NativeVLAN  string `json:"nativeVlanId"`
	MappedID    string `json:"mappedPhysicalInterfaceId"`
}

// This endpoint returns a device-scoped array and documents no pagination
// arguments. Reuse transport/decoding, but do not invent offset/limit semantics.
func (c *client) lanLinkInterfaces(ctx context.Context, deviceID string) ([]lanLinkInterface, error) {
	if !validAPIID(deviceID) {
		return nil, errInvalidResponse
	}
	body, err := c.get(ctx, "/dna/intent/api/v1/interface/network-device/"+url.PathEscape(deviceID), nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	rows, err := decodeList[lanLinkInterface](body)
	if err != nil {
		return nil, err
	}
	if len(rows) > 10000 {
		return nil, errInvalidResponse
	}
	ids, names, indices := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, row := range rows {
		name := strings.ToLower(row.PortName)
		if !validAPIID(row.ID) || row.DeviceID != deviceID || !validLANInterfaceName(row.PortName) || row.Index == "" || len(row.Index) > 32 || ids[row.ID] || names[name] || indices[row.Index] {
			return nil, errInvalidResponse
		}
		for _, r := range row.Index {
			if r < '0' || r > '9' {
				return nil, errInvalidResponse
			}
		}
		ids[row.ID], names[name], indices[row.Index] = true, true, true
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func validLANInterfaceName(name string) bool {
	return name != "" && len(name) <= 256 && !strings.ContainsFunc(name, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) })
}

func bindLANLinkInterface(deviceID, name string, rows []lanLinkInterface) (lanLinkInterface, error) {
	var selected *lanLinkInterface
	for i := range rows {
		row := &rows[i]
		// Exact native port spelling is required. Interface abbreviations and
		// mapped virtual interfaces cannot establish a physical port identity.
		if row.PortName != name {
			continue
		}
		if selected != nil || row.DeviceID != deviceID || !validAPIID(row.ID) || row.Type != "Physical" || row.MappedID != "" {
			return lanLinkInterface{}, errConfigurationDeviceAdmission
		}
		selected = row
	}
	if selected == nil {
		return lanLinkInterface{}, errConfigurationBusy
	}
	return *selected, nil
}
