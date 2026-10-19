// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// ID is a NetAsCode discriminator only. It must never be used as a native
// port-channel number or UUID. Optional settings retain omission semantics.
type fabricPortChannelIntent struct {
	ID           *int64   `json:"id"`
	Type         string   `json:"connected_device_type"`
	Protocol     *string  `json:"protocol"`
	Description  *string  `json:"description"`
	Interfaces   []string `json:"interface_names"`
	NativeVLAN   *int     `json:"native_vlan_id"`
	AllowedVLANs *string  `json:"allowed_vlan_ranges"`
}

type fabricPortChannelDeploymentIntent struct {
	device  fabricDeviceIntent
	channel fabricPortChannelIntent
}

// Parse the shared strict edge-device envelope, then validate channel-specific
// identity and member constraints without discarding other device effects.
func parseFabricPortChannelDeployment(raw []byte) ([]fabricDeviceIntent, []fabricPortChannelDeploymentIntent, error) {
	base, channels, err := parseFabricEdgeExtensions(raw, "port_channels", func(rows []fabricPortChannelIntent) ([]fabricPortChannelIntent, error) {
		ids := map[int64]bool{}
		ports := map[string]bool{}
		for i := range rows {
			row := &rows[i]
			if row.validate() != nil || ids[*row.ID] {
				return nil, errInvalidResponse
			}
			ids[*row.ID] = true
			for _, port := range row.Interfaces {
				key := strings.ToLower(port)
				if ports[key] {
					return nil, errInvalidResponse
				}
				ports[key] = true
			}
			sort.Strings(row.Interfaces)
		}
		sort.Slice(rows, func(i, j int) bool { return *rows[i].ID < *rows[j].ID })
		return rows, nil
	})
	if err != nil {
		return nil, nil, err
	}
	out := []fabricPortChannelDeploymentIntent{}
	for _, device := range base {
		for _, row := range channels[device.inventory.reference.Name] {
			out = append(out, fabricPortChannelDeploymentIntent{device: device, channel: row})
		}
	}
	return base, out, nil
}

func (want fabricPortChannelIntent) validate() error {
	if want.ID == nil || (want.Type != "TRUNK" && want.Type != "EXTENDED_NODE") || len(want.Interfaces) == 0 || len(want.Interfaces) > 16 {
		return errInvalidResponse
	}
	if want.Protocol != nil {
		if !slices.Contains([]string{"ON", "LACP", "PAGP"}, *want.Protocol) {
			return errInvalidResponse
		}
		if *want.Protocol != "LACP" && len(want.Interfaces) > 8 {
			return errInvalidResponse
		}
		if want.Type == "EXTENDED_NODE" && *want.Protocol != "PAGP" {
			return errInvalidResponse
		}
	}
	if want.NativeVLAN != nil && (*want.NativeVLAN < 1 || *want.NativeVLAN > 4094 || want.Type != "TRUNK") {
		return errInvalidResponse
	}
	if want.AllowedVLANs != nil && (want.Type != "TRUNK" || len(*want.AllowedVLANs) > 32768 || *want.AllowedVLANs == "" || strings.ContainsFunc(*want.AllowedVLANs, unicode.IsControl)) {
		return errInvalidResponse
	}
	if want.Description != nil && (len(*want.Description) > 4096 || strings.ContainsFunc(*want.Description, unicode.IsControl)) {
		return errInvalidResponse
	}
	seen := map[string]bool{}
	for _, port := range want.Interfaces {
		key := strings.ToLower(port)
		if !validLANInterfaceName(port) || seen[key] {
			return errInvalidResponse
		}
		seen[key] = true
	}
	return nil
}

// Resolve every channel member through the existing exact physical-interface
// reader. This binds observed identities only; no mutation authority is granted.
func bindFabricPortChannelInterfaces(deviceID string, want fabricPortChannelIntent, rows []lanLinkInterface) ([]lanLinkInterface, error) {
	if !validAPIID(deviceID) || want.validate() != nil {
		return nil, errInvalidResponse
	}
	out := make([]lanLinkInterface, 0, len(want.Interfaces))
	seen := map[string]bool{}
	for _, name := range want.Interfaces {
		port, err := bindLANLinkInterface(deviceID, name, rows)
		if err != nil {
			return nil, err
		}
		if seen[port.ID] {
			return nil, errInvalidResponse
		}
		seen[port.ID] = true
		out = append(out, port)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Private payload construction requires resolved UUIDs and physical port
// evidence. Fabric membership, native channel ownership and admission still
// belong to the future coordinator; this function performs no submission.
func fabricPortChannelPayload(deviceID, fabricID string, want fabricPortChannelIntent, ports []lanLinkInterface) ([]byte, error) {
	if !validAPIID(fabricID) {
		return nil, errInvalidResponse
	}
	if _, err := bindFabricPortChannelInterfaces(deviceID, want, ports); err != nil {
		return nil, err
	}
	type payload struct {
		Device       string   `json:"networkDeviceId"`
		Fabric       string   `json:"fabricId"`
		Interfaces   []string `json:"interfaceNames"`
		Type         string   `json:"connectedDeviceType"`
		Protocol     *string  `json:"protocol,omitempty"`
		Description  *string  `json:"description,omitempty"`
		NativeVLAN   *int     `json:"nativeVlanId,omitempty"`
		AllowedVLANs *string  `json:"allowedVlanRanges,omitempty"`
	}
	return json.Marshal([]payload{{Device: deviceID, Fabric: fabricID, Interfaces: slices.Clone(want.Interfaces), Type: want.Type, Protocol: want.Protocol, Description: want.Description, NativeVLAN: want.NativeVLAN, AllowedVLANs: want.AllowedVLANs}})
}
