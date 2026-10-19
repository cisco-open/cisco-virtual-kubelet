// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
)

type fabricL2HandoffBinding struct {
	desired                   fabricL2Handoff
	dependencyID, interfaceID string
	digest                    string
}

// Resolve the pinned named dependency against observed native objects. The
// inventory compiler must establish deviceName-to-device UUID/serial identity
// before calling this projection, then revalidate the full snapshot at dispatch.
func bindFabricL2Handoff(want fabricL2HandoffIntent, deviceName string, device fabricDeviceConfiguration, ports []lanLinkInterface, gateways []anycastGateway, networks []fabricL2Network) (fabricL2HandoffBinding, error) {
	reject := errConfigurationDeviceAdmission
	empty := fabricL2HandoffBinding{}
	if want.device != deviceName || !validProfileName(deviceName) {
		return empty, reject
	}
	native, err := canonicalFabricDevice(device, false)
	if err != nil || !slices.Contains(native.Roles, "BORDER_NODE") || native.Border == nil || !slices.Contains(native.Border.Types, "LAYER_2") {
		return empty, reject
	}
	port, err := bindLANLinkInterface(native.NetworkDeviceID, want.port, ports)
	if err != nil {
		return empty, err
	}
	out := fabricL2HandoffBinding{desired: fabricL2Handoff{DeviceID: native.NetworkDeviceID, FabricID: native.FabricID, Port: port.PortName, ExternalVLAN: want.externalVLAN}, interfaceID: port.ID}
	var dependency any
	if want.vlanName != "" {
		if want.network != "" || want.pool != "" {
			return empty, reject
		}
		for _, network := range networks {
			if network.FabricID != native.FabricID || network.VLANName != want.vlanName {
				continue
			}
			if out.dependencyID != "" || !validAPIID(network.ID) {
				return empty, reject
			}
			out.dependencyID = network.ID
			out.desired.InternalVLAN = network.VLAN
			dependency = network
		}
		for _, gateway := range gateways {
			if gateway.FabricID == native.FabricID && gateway.VLAN == out.desired.InternalVLAN {
				return empty, reject
			}
		}
	} else {
		if !validProfileName(want.network) || !validProfileName(want.pool) {
			return empty, reject
		}
		for _, gateway := range gateways {
			if gateway.FabricID != native.FabricID || gateway.Network != want.network || gateway.Pool != want.pool {
				continue
			}
			if out.dependencyID != "" || !validAPIID(gateway.ID) || gateway.unknownFields {
				return empty, reject
			}
			out.dependencyID = gateway.ID
			out.desired.InternalVLAN = gateway.VLAN
			dependency = []any{gateway.ID, gateway.FabricID, gateway.Network, gateway.Pool, gateway.VLANName, gateway.VLAN, gateway.fields, gateway.secondaryKnown}
		}
	}
	if out.dependencyID == "" || !validFabricL2Handoff(out.desired) {
		return empty, reject
	}
	raw, err := json.Marshal([]any{deviceName, native, port, out.desired, out.dependencyID, dependency})
	if err != nil {
		return empty, errInvalidResponse
	}
	out.digest = fmt.Sprintf("%x", sha256.Sum256(raw))
	return out, nil
}
