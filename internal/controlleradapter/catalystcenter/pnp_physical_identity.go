// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
	"sort"
	"strings"
)

// Resolve a complete declared physical set in PnP, excluding hardware already
// present in network inventory. Shared by LAN discovery and claim preflight;
// these observations do not grant mutation admission or attest readiness.
func bindPnPPhysicalDevices(serials []string, pnp []pnpDevice, inventory []Device) ([]pnpDevice, error) {
	if len(serials) == 0 || len(serials) > 1000 {
		return nil, errConfigurationDeviceAdmission
	}
	requested := map[string]bool{}
	for _, value := range serials {
		serial, err := topologyidentity.CanonicalPhysicalIdentity(value)
		if err != nil || requested[serial] {
			return nil, errInvalidResponse
		}
		requested[serial] = true
	}
	// Existing managed hardware cannot be treated as a fresh PnP target.
	for _, device := range inventory {
		for _, part := range strings.Split(device.Serial, ",") {
			serial, err := topologyidentity.CanonicalPhysicalIdentity(part)
			if err == nil && requested[serial] {
				return nil, errConfigurationOwned
			}
		}
	}
	owners := map[string]string{}
	ids := map[string]bool{}
	selected := []pnpDevice{}
	for _, device := range pnp {
		if !validAPIID(device.ID) || ids[device.ID] || device.Info == nil {
			return nil, errInvalidResponse
		}
		ids[device.ID] = true
		serial, err := topologyidentity.CanonicalPhysicalIdentity(device.Info.Serial)
		if err != nil {
			return nil, errInvalidResponse
		}
		members := map[string]bool{serial: true}
		for _, member := range device.Info.StackInfo.Members {
			serial, err := topologyidentity.CanonicalPhysicalIdentity(member.Serial)
			if err != nil {
				return nil, errInvalidResponse
			}
			members[serial] = true
		}
		matches := 0
		for serial := range members {
			if owner, exists := owners[serial]; exists && owner != device.ID {
				return nil, errInvalidResponse
			}
			owners[serial] = device.ID
			if requested[serial] {
				matches++
			}
		}
		if matches == 0 {
			continue
		}
		if matches != len(members) {
			return nil, errConfigurationDeviceAdmission
		}
		selected = append(selected, device)
	}
	for serial := range requested {
		if owners[serial] == "" {
			return nil, errConfigurationBusy
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].ID < selected[j].ID })
	return selected, nil
}
