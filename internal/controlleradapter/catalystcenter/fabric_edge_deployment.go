// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

type fabricEdgeDeployment struct {
	devices                         []fabricDeviceIntent
	channels                        []fabricPortChannelDeploymentIntent
	assignments                     []fabricPortAssignmentDeploymentIntent
	channelSource, assignmentSource []byte
}

// Split only the two supported extensions, then consume both through their
// strict existing parsers. Every other source field survives both projections.
func parseFabricEdgeDeployment(raw []byte) (*fabricEdgeDeployment, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return nil, errInvalidResponse
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		return nil, errInvalidResponse
	}
	out := &fabricEdgeDeployment{}
	for _, extension := range []string{"port_channels", "port_assignments"} {
		var projection map[string]any
		if json.Unmarshal(encoded, &projection) != nil {
			return nil, errInvalidResponse
		}
		inventory, ok := projection["inventory"].(map[string]any)
		if !ok {
			return nil, errInvalidResponse
		}
		devices, ok := inventory["devices"].([]any)
		if !ok {
			return nil, errInvalidResponse
		}
		present := false
		other := "port_assignments"
		if extension == other {
			other = "port_channels"
		}
		for _, value := range devices {
			device, ok := value.(map[string]any)
			if !ok {
				return nil, errInvalidResponse
			}
			if _, ok := device[extension]; ok {
				present = true
			}
			delete(device, other)
		}
		if !present {
			continue
		}
		source, err := json.Marshal(map[string]any{"catalyst_center": projection})
		if err != nil {
			return nil, errInvalidResponse
		}
		if extension == "port_channels" {
			out.devices, out.channels, err = parseFabricPortChannelDeployment(source)
			out.channelSource = source
		} else {
			out.devices, out.assignments, err = parseFabricPortAssignmentDeployment(source)
			out.assignmentSource = source
		}
		if err != nil {
			return nil, err
		}
	}
	if len(out.channels)+len(out.assignments) == 0 {
		return nil, errInvalidResponse
	}
	channelPorts := map[string]map[string]bool{}
	for _, intent := range out.channels {
		name := intent.device.inventory.reference.Name
		if channelPorts[name] == nil {
			channelPorts[name] = map[string]bool{}
		}
		for _, port := range intent.channel.Interfaces {
			channelPorts[name][strings.ToLower(port)] = true
		}
	}
	for _, intent := range out.assignments {
		for _, row := range intent.assignments {
			if channelPorts[intent.device.inventory.reference.Name][strings.ToLower(*row.Interface)] {
				return nil, errConfigurationStale
			}
		}
	}
	return out, nil
}
