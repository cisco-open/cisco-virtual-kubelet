// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
)

type fabricSDAHandoffBinding struct {
	desired fabricSDAHandoff
	transit fabricTransit
	digest  string
}

// Project validated observed identities, retaining transit control-plane evidence
// for the later physical dependency collector. This grants no mutation authority.
func bindFabricSDAHandoff(want fabricSDAHandoffIntent, deviceName string, device fabricDeviceConfiguration, transits map[string]fabricTransit) (fabricSDAHandoffBinding, error) {
	empty := fabricSDAHandoffBinding{}
	if want.Device != deviceName || !validProfileName(deviceName) || !validProfileName(want.Transit) || !validFabricSDAAffinity(want.Prime) || !validFabricSDAAffinity(want.Decider) {
		return empty, errInvalidResponse
	}
	native, err := canonicalFabricDevice(device, false)
	if err != nil || !slices.Contains(native.Roles, "BORDER_NODE") || native.Border == nil || !slices.Contains(native.Border.Types, "LAYER_3") {
		return empty, errConfigurationDeviceAdmission
	}
	transit, ok := transits[want.Transit]
	if !ok || transit.Name != want.Transit || !validAPIID(transit.ID) || (transit.Type != "SDA_LISP_PUB_SUB_TRANSIT" && transit.Type != "SDA_LISP_BGP_TRANSIT") || transit.IP != nil || transit.SDA == nil || len(transit.SDA.ControlPlanes) == 0 || len(transit.SDA.ControlPlanes) > 1000 {
		return empty, errConfigurationBusy
	}
	owned := *transit.SDA
	owned.ControlPlanes = slices.Clone(owned.ControlPlanes)
	slices.Sort(owned.ControlPlanes)
	for i, id := range owned.ControlPlanes {
		if !validAPIID(id) || i > 0 && owned.ControlPlanes[i-1] == id {
			return empty, errInvalidResponse
		}
	}
	if owned.Multicast != nil {
		value := *owned.Multicast
		owned.Multicast = &value
	}
	transit.SDA = &owned
	if want.Multicast != nil && *want.Multicast && (transit.Type != "SDA_LISP_PUB_SUB_TRANSIT" || owned.Multicast == nil || !*owned.Multicast) {
		return empty, errConfigurationBusy
	}
	row := fabricSDAHandoff{DeviceID: native.NetworkDeviceID, FabricID: native.FabricID, TransitID: transit.ID}
	if want.Prime != nil {
		value := *want.Prime
		row.Prime = &value
	}
	if want.Decider != nil {
		value := *want.Decider
		row.Decider = &value
	}
	if want.Internet != nil {
		value := *want.Internet
		row.Internet = &value
	}
	if want.Multicast != nil {
		value := *want.Multicast
		row.Multicast = &value
	}
	raw, err := json.Marshal([]any{deviceName, native, transit, row})
	if err != nil {
		return empty, errInvalidResponse
	}
	return fabricSDAHandoffBinding{desired: row, transit: transit, digest: fmt.Sprintf("%x", sha256.Sum256(raw))}, nil
}
