// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
)

type fabricL3IPHandoffBinding struct {
	desired fabricL3IPHandoff
	digest  string
}

// Bind already observed controller objects; this is not mutation admission.
// External-pool reservation ownership/allocation must be checked separately by
// the dependency collector before this projection may be used for dispatch.
func bindFabricL3IPHandoff(want fabricL3IPHandoffIntent, deviceName string, device fabricDeviceConfiguration, ports []lanLinkInterface, transits map[string]fabricTransit, networks map[string]fabricL3Network) (fabricL3IPHandoffBinding, error) {
	empty := fabricL3IPHandoffBinding{}
	if want.device != deviceName || !validProfileName(deviceName) || !validProfileName(want.transit) {
		return empty, errInvalidResponse
	}
	native, err := canonicalFabricDevice(device, false)
	if err != nil || !slices.Contains(native.Roles, "BORDER_NODE") || native.Border == nil || !slices.Contains(native.Border.Types, "LAYER_3") {
		return empty, errConfigurationDeviceAdmission
	}
	port, err := bindLANLinkInterface(native.NetworkDeviceID, want.port, ports)
	if err != nil {
		return empty, err
	}
	transit, found := transits[want.transit]
	if !found || transit.Name != want.transit || !validAPIID(transit.ID) || transit.Type != "IP_BASED_TRANSIT" || transit.IP == nil || transit.IP.Protocol != "BGP" {
		return empty, errConfigurationBusy
	}
	asn, err := transitASN(transit.IP.ASN)
	if err != nil {
		return empty, errInvalidResponse
	}
	// Own the canonical value; never mutate inventory through a shared pointer.
	transit.IP = &ipTransitSettings{Protocol: "BGP", ASN: asn}
	network, found := networks[want.network]
	if !found || network.Name != want.network || !validAPIID(network.ID) || !network.membershipsKnown || !slices.Contains(network.Fabrics, native.FabricID) || network.Anchor != "" {
		return empty, errConfigurationBusy
	}
	network.Fabrics = slices.Clone(network.Fabrics)
	slices.Sort(network.Fabrics)
	for i, id := range network.Fabrics {
		if !validAPIID(id) || i > 0 && network.Fabrics[i-1] == id {
			return empty, errInvalidResponse
		}
	}
	row := fabricL3IPHandoff{DeviceID: native.NetworkDeviceID, FabricID: native.FabricID, TransitID: transit.ID, Port: port.PortName, Network: network.Name, VLAN: want.vlan, Pool: want.pool}
	if want.mss != nil {
		mss := *want.mss
		row.MSS = &mss
	}
	// Match pinned cc_fabric.tf: a selected external pool suppresses explicit
	// local/peer addresses in the native request, without modifying the intent.
	if want.pool == "" {
		row.Local4, row.Peer4, row.Local6, row.Peer6 = want.local4, want.peer4, want.local6, want.peer6
	}
	if !validFabricL3IPHandoff(row) {
		return empty, errInvalidResponse
	}
	raw, err := json.Marshal([]any{deviceName, native, port, transit, network, row})
	if err != nil {
		return empty, errInvalidResponse
	}
	return fabricL3IPHandoffBinding{desired: row, digest: fmt.Sprintf("%x", sha256.Sum256(raw))}, nil
}
