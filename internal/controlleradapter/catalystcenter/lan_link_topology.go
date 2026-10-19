// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

const lanPhysicalTopologyPath = "/dna/intent/api/v1/topology/physical-topology"

type lanTopologyNode struct {
	ID     string `json:"id"`
	IP     string `json:"ip"`
	Type   string `json:"nodeType"`
	Greyed bool   `json:"greyOut"`
}

// Omit volatile speeds, labels, counters and layout. Keep the device/port and
// addressing evidence required to identify exactly one physical adjacency.
type lanPhysicalLink struct {
	ID        string `json:"id"`
	Source    string `json:"source"`
	Target    string `json:"target"`
	StartID   string `json:"startPortID"`
	EndID     string `json:"endPortID"`
	StartName string `json:"startPortName"`
	EndName   string `json:"endPortName"`
	Status    string `json:"linkStatus"`
	StartIP   string `json:"startPortIpv4Address,omitempty"`
	StartMask string `json:"startPortIpv4Mask,omitempty"`
	EndIP     string `json:"endPortIpv4Address,omitempty"`
	EndMask   string `json:"endPortIpv4Mask,omitempty"`
	Greyed    bool   `json:"greyOut"`
}

type lanPhysicalTopology struct {
	Nodes []lanTopologyNode `json:"nodes"`
	Links []lanPhysicalLink `json:"links"`
}

// IDs are opaque topology identifiers, not path parameters. Link IDs may be
// compound strings; do not force the narrower native resource UUID alphabet.
func validLANTopologyID(value string) bool {
	return len(value) <= 512 && validLANObservationText(value) && !strings.ContainsAny(value, "\t \r\n")
}

// This API documents no pagination. Require the complete HTTP 200 object;
// absent/null arrays and partial/no-content responses cannot prove absence.
func (c *client) lanPhysicalTopology(ctx context.Context) (*lanPhysicalTopology, error) {
	raw, err := c.get(ctx, lanPhysicalTopologyPath, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var body struct {
		Response *lanPhysicalTopology `json:"response"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Response == nil || body.Response.Nodes == nil || body.Response.Links == nil || len(body.Response.Nodes) > 100000 || len(body.Response.Links) > 200000 {
		return nil, errInvalidResponse
	}
	topology := body.Response
	nodes := map[string]bool{}
	for _, node := range topology.Nodes {
		if !validLANTopologyID(node.ID) || !validLANObservationText(node.Type) || nodes[node.ID] {
			return nil, errInvalidResponse
		}
		nodes[node.ID] = true
	}
	links := map[string]bool{}
	for _, link := range topology.Links {
		if !validLANTopologyID(link.ID) || links[link.ID] || !nodes[link.Source] || !nodes[link.Target] || !validLANTopologyID(link.StartID) || !validLANTopologyID(link.EndID) || !validLANInterfaceName(link.StartName) || !validLANInterfaceName(link.EndName) || !validLANObservationText(link.Status) {
			return nil, errInvalidResponse
		}
		links[link.ID] = true
	}
	sort.Slice(topology.Nodes, func(i, j int) bool { return topology.Nodes[i].ID < topology.Nodes[j].ID })
	sort.Slice(topology.Links, func(i, j int) bool { return topology.Links[i].ID < topology.Links[j].ID })
	return topology, nil
}

// Unknown link status remains observation, never permission or successful
// execution evidence. Both directions bind the same physical endpoints.
func bindLANLinkAdjacency(endpoints *lanLinkEndpointSnapshot, topology *lanPhysicalTopology) (*lanPhysicalLink, error) {
	if endpoints == nil || topology == nil || topology.Nodes == nil || topology.Links == nil {
		return nil, errInvalidResponse
	}
	for _, endpoint := range []lanLinkEndpoint{endpoints.primary, endpoints.secondary} {
		found := false
		for _, node := range topology.Nodes {
			if node.ID != endpoint.device.ID {
				continue
			}
			if found || node.Type != "device" || node.Greyed || !sameManagementIP(node.IP, endpoint.device.ManagementIP) {
				return nil, errConfigurationStale
			}
			found = true
		}
		if !found {
			return nil, errConfigurationBusy
		}
	}
	touches := func(deviceID, portID, name string, endpoint lanLinkEndpoint) bool {
		return portID == endpoint.port.ID || deviceID == endpoint.device.ID && strings.EqualFold(name, endpoint.port.PortName)
	}
	matches := func(deviceID, portID, name string, endpoint lanLinkEndpoint) bool {
		return deviceID == endpoint.device.ID && portID == endpoint.port.ID && name == endpoint.port.PortName
	}
	var selected *lanPhysicalLink
	for _, link := range topology.Links {
		incident := false
		for _, endpoint := range []lanLinkEndpoint{endpoints.primary, endpoints.secondary} {
			incident = incident || touches(link.Source, link.StartID, link.StartName, endpoint) || touches(link.Target, link.EndID, link.EndName, endpoint)
		}
		if !incident {
			continue
		}
		forward := matches(link.Source, link.StartID, link.StartName, endpoints.primary) && matches(link.Target, link.EndID, link.EndName, endpoints.secondary)
		reverse := matches(link.Source, link.StartID, link.StartName, endpoints.secondary) && matches(link.Target, link.EndID, link.EndName, endpoints.primary)
		if (!forward && !reverse) || selected != nil {
			return nil, errConfigurationDeviceAdmission
		}
		if link.Greyed {
			return nil, errConfigurationBusy
		}
		copy := link
		if reverse {
			copy.Source, copy.Target = copy.Target, copy.Source
			copy.StartID, copy.EndID = copy.EndID, copy.StartID
			copy.StartName, copy.EndName = copy.EndName, copy.StartName
			copy.StartIP, copy.EndIP = copy.EndIP, copy.StartIP
			copy.StartMask, copy.EndMask = copy.EndMask, copy.StartMask
		}
		selected = &copy
	}
	return selected, nil
}
