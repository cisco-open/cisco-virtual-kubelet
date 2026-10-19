// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

type fabricL3IPHandoffIntent struct {
	device, transit, port, network, pool string
	vlan                                 int
	local4, peer4, local6, peer6         string
	mss                                  *int
}

// Parse the pinned 0.5.0 border l3_handoffs projection. Names are dependency
// references, not native UUIDs. Pool selection and explicit addresses remain
// distinct here; native projection must honor the module's pool precedence.
func parseFabricL3IPHandoffs(device, pool string, value any) ([]fabricL3IPHandoffIntent, error) {
	if !validProfileName(device) || pool != "" && !validProfileName(pool) || value == nil {
		return nil, errInvalidResponse
	}
	var model []struct {
		Name       string `json:"name"`
		Interfaces []struct {
			Name     string `json:"name"`
			Networks []struct {
				Name   string `json:"name"`
				VLAN   int    `json:"vlan"`
				Local4 string `json:"local_ip_address"`
				Peer4  string `json:"peer_ip_address"`
				Local6 string `json:"local_ipv6_address"`
				Peer6  string `json:"peer_ipv6_address"`
				MSS    *int   `json:"tcp_mss_adjustment"`
			} `json:"virtual_networks"`
		} `json:"interfaces"`
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, errInvalidResponse
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&model) != nil || len(model) > 1000 {
		return nil, errInvalidResponse
	}
	out := []fabricL3IPHandoffIntent{}
	transits, mappings, tags := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, transit := range model {
		if !validProfileName(transit.Name) || transits[transit.Name] || len(transit.Interfaces) == 0 || len(transit.Interfaces) > 1000 {
			return nil, errInvalidResponse
		}
		transits[transit.Name] = true
		ports := map[string]bool{}
		for _, port := range transit.Interfaces {
			canonical := strings.ToLower(port.Name)
			if !validLANInterfaceName(port.Name) || ports[canonical] || len(port.Networks) == 0 || len(port.Networks) > 1000 {
				return nil, errInvalidResponse
			}
			ports[canonical] = true
			for _, network := range port.Networks {
				key := transit.Name + "\x00" + canonical + "\x00" + network.Name
				tag := canonical + "\x00" + strconv.Itoa(network.VLAN)
				if !validProfileName(network.Name) || !validFabricVLAN(network.VLAN) || network.MSS != nil && (*network.MSS < 500 || *network.MSS > 1440) || len(out) >= 1000 || mappings[key] || tags[tag] {
					return nil, errInvalidResponse
				}
				for _, address := range []struct {
					value string
					ipv4  bool
				}{{network.Local4, true}, {network.Peer4, true}, {network.Local6, false}, {network.Peer6, false}} {
					if address.value == "" {
						continue
					}
					if !validFabricHandoffPrefix(address.value, address.ipv4) {
						return nil, errInvalidResponse
					}
				}
				mappings[key], tags[tag] = true, true
				out = append(out, fabricL3IPHandoffIntent{device: device, transit: transit.Name, port: port.Name, network: network.Name, pool: pool, vlan: network.VLAN, local4: network.Local4, peer4: network.Peer4, local6: network.Local6, peer6: network.Peer6, mss: network.MSS})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].transit != out[j].transit {
			return out[i].transit < out[j].transit
		}
		if out[i].port != out[j].port {
			return out[i].port < out[j].port
		}
		return out[i].network < out[j].network
	})
	return out, nil
}

func validFabricHandoffPrefix(value string, ipv4 bool) bool {
	if value == "" {
		return true
	}
	prefix, err := netip.ParsePrefix(value)
	return err == nil && prefix.Addr().Is4() == ipv4 && !prefix.Addr().Is4In6() && !prefix.Addr().IsMulticast() && !prefix.Addr().IsUnspecified()
}
