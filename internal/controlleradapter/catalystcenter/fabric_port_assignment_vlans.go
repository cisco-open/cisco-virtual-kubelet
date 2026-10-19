// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"sort"
)

// Keep the complete observed reference evidence, including both native views
// when an anycast gateway and L2 network describe the same VLAN. This grants no
// write or adoption authority over either prerequisite.
type fabricPortAssignmentVLANBinding struct {
	Name    string
	Traffic string
	VLAN    int
	L2      *fabricL2Network
	Gateway json.RawMessage
}

func resolveFabricPortAssignmentVLANs(fabricID string, wants []fabricPortAssignmentIntent, l2 map[fabricL2Key]fabricL2Network, gateways map[anycastGatewayKey]anycastGateway) ([]fabricPortAssignmentVLANBinding, error) {
	if !validAPIID(fabricID) {
		return nil, errInvalidResponse
	}
	rows, err := expandFabricPortAssignments(wants)
	if err != nil || len(rows) == 0 {
		return nil, errInvalidResponse
	}
	required := map[string]string{}
	for _, row := range rows {
		for _, ref := range []struct {
			name    *string
			traffic string
		}{{row.DataVLAN, "DATA"}, {row.VoiceVLAN, "VOICE"}} {
			if ref.name == nil {
				continue
			}
			if before, ok := required[*ref.name]; ok && before != ref.traffic {
				return nil, errConfigurationStale
			}
			required[*ref.name] = ref.traffic
		}
	}
	return resolveFabricVLANReferences(fabricID, required, l2, gateways)
}

// Shared native VLAN binding for wired ports and wireless SSID mapping. Callers
// impose their own feature prerequisites on the retained native observations.
func resolveFabricVLANReferences(fabricID string, required map[string]string, l2 map[fabricL2Key]fabricL2Network, gateways map[anycastGatewayKey]anycastGateway) ([]fabricPortAssignmentVLANBinding, error) {
	if !validAPIID(fabricID) {
		return nil, errInvalidResponse
	}
	for name, traffic := range required {
		if !validProfileName(name) || traffic != "DATA" && traffic != "VOICE" {
			return nil, errInvalidResponse
		}
	}
	var err error
	out := []fabricPortAssignmentVLANBinding{}
	for name, traffic := range required {
		binding := fabricPortAssignmentVLANBinding{Name: name, Traffic: traffic}
		if network, ok := l2[fabricL2Key{fabricID, name}]; ok {
			if network.FabricID != fabricID || network.VLANName != name || !validAPIID(network.ID) || !validFabricVLAN(network.VLAN) || network.Traffic != traffic {
				return nil, errConfigurationStale
			}
			binding.VLAN = network.VLAN
			binding.L2 = &network
		}
		for _, gateway := range gateways {
			if gateway.FabricID != fabricID || gateway.VLANName != name {
				continue
			}
			if binding.Gateway != nil || !validAPIID(gateway.ID) || !validFabricVLAN(gateway.VLAN) || gateway.fields["trafficType"] != traffic || binding.VLAN != 0 && binding.VLAN != gateway.VLAN {
				return nil, errConfigurationStale
			}
			binding.VLAN = gateway.VLAN
			// Preserve native UUID and all parsed settings in deterministic evidence.
			binding.Gateway, err = json.Marshal(gateway.fields)
			if err != nil {
				return nil, errInvalidResponse
			}
		}
		if binding.VLAN == 0 {
			return nil, errConfigurationBusy
		}
		out = append(out, binding)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (c *client) fabricPortAssignmentVLANs(ctx context.Context, fabricID string, wants []fabricPortAssignmentIntent) ([]fabricPortAssignmentVLANBinding, error) {
	rows, err := expandFabricPortAssignments(wants)
	if err != nil || len(rows) == 0 || !validAPIID(fabricID) {
		return nil, errInvalidResponse
	}
	references := false
	for _, row := range rows {
		references = references || row.DataVLAN != nil || row.VoiceVLAN != nil
	}
	if !references {
		return []fabricPortAssignmentVLANBinding{}, nil
	}
	l2, err := c.fabricL2Networks(ctx)
	if err != nil {
		return nil, err
	}
	gateways, err := c.anycastGateways(ctx)
	if err != nil {
		return nil, err
	}
	return resolveFabricPortAssignmentVLANs(fabricID, rows, l2, gateways)
}
