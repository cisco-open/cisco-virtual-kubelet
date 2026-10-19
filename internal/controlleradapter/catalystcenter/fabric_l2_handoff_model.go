// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

type fabricL2HandoffIntent struct {
	device, port, network, pool, vlanName string
	externalVLAN                          int
}

// Parse one border device's pinned NetAsCode l2_handoffs projection. Dependency
// names remain unresolved; they are never sent to the native API as VLAN IDs.
func parseFabricL2Handoffs(device string, value any) ([]fabricL2HandoffIntent, error) {
	if !validProfileName(device) || value == nil {
		return nil, errInvalidResponse
	}
	var model struct {
		Anycast []struct {
			Network    string   `json:"l3_virtual_network"`
			Pool       string   `json:"ip_pool_name"`
			External   int      `json:"external_vlan"`
			Interfaces []string `json:"interfaces"`
		} `json:"l2_with_anycast_gateway"`
		Layer2 *struct {
			VLANs []struct {
				Name     string `json:"name"`
				External int    `json:"external_vlan"`
			} `json:"vlans"`
			Interfaces []string `json:"interfaces"`
		} `json:"l2_without_anycast_gateway"`
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, errInvalidResponse
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&model) != nil || len(model.Anycast) > 1000 {
		return nil, errInvalidResponse
	}
	out := []fabricL2HandoffIntent{}
	external, dependencies := map[string]bool{}, map[string]bool{}
	add := func(base fabricL2HandoffIntent, ports []string) error {
		if base.externalVLAN < 1 || base.externalVLAN > 4094 || len(ports) == 0 || len(ports) > 1000 {
			return errInvalidResponse
		}
		for _, port := range ports {
			if !validLANInterfaceName(port) || len(out) >= 1000 {
				return errInvalidResponse
			}
			canonical := strings.ToLower(port)
			key := canonical + "\x00" + strconv.Itoa(base.externalVLAN)
			dep := canonical + "\x00" + base.network + "\x00" + base.pool + "\x00" + base.vlanName
			if external[key] || dependencies[dep] {
				return errInvalidResponse
			}
			external[key], dependencies[dep] = true, true
			row := base
			row.device = device
			row.port = port
			out = append(out, row)
		}
		return nil
	}
	for _, v := range model.Anycast {
		if !validProfileName(v.Network) || !validProfileName(v.Pool) {
			return nil, errInvalidResponse
		}
		if err := add(fabricL2HandoffIntent{network: v.Network, pool: v.Pool, externalVLAN: v.External}, v.Interfaces); err != nil {
			return nil, err
		}
	}
	if model.Layer2 != nil {
		if len(model.Layer2.VLANs) == 0 || len(model.Layer2.VLANs) > 1000 {
			return nil, errInvalidResponse
		}
		for _, v := range model.Layer2.VLANs {
			if !validFabricVLANName(v.Name) {
				return nil, errInvalidResponse
			}
			if err := add(fabricL2HandoffIntent{vlanName: v.Name, externalVLAN: v.External}, model.Layer2.Interfaces); err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].port != out[j].port {
			return out[i].port < out[j].port
		}
		return out[i].externalVLAN < out[j].externalVLAN
	})
	return out, nil
}
