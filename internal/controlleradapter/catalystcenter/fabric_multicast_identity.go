// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"sort"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

// Resolve exact/FQDN or unambiguous short hostnames using the same matching
// convention as managed wireless anchors. This grants no mutation admission.
func resolveMulticastRPNames(wants []fabricMulticastVNIntent, inventory []Device) (map[string]string, error) {
	names := map[string]bool{}
	for _, want := range wants {
		for _, rp := range want.rps {
			for _, name := range rp.devices {
				names[name] = true
			}
		}
	}
	out := map[string]string{}
	for name := range names {
		if !validProfileName(name) {
			return nil, errInvalidResponse
		}
		matches := []Device{}
		for _, device := range inventory {
			if device.Hostname == name || !strings.Contains(name, ".") && strings.SplitN(device.Hostname, ".", 2)[0] == name {
				matches = append(matches, device)
			}
		}
		if len(matches) != 1 {
			return nil, errConfigurationStale
		}
		target, err := resolveConfigurationDevice(configurationDeviceReference{Name: matches[0].Hostname, Serial: matches[0].Serial}, inventory)
		if err != nil {
			return nil, err
		}
		bound, err := bindMulticastRPDevices([]string{target.ID}, inventory)
		if err != nil || len(bound) != 1 {
			return nil, errConfigurationStale
		}
		out[name] = bound[0].ID
	}
	return out, nil
}

// Also bind retained RPs by UUID. A retained VN may have no name reference in
// this manifest, but its device replacement must still invalidate the evidence.
func bindMulticastRPDevices(ids []string, inventory []Device) ([]Device, error) {
	byID := map[string]Device{}
	serialCount := map[string]int{}
	for _, device := range inventory {
		if !validAPIID(device.ID) {
			return nil, errInvalidResponse
		}
		if _, exists := byID[device.ID]; exists {
			return nil, errInvalidResponse
		}
		byID[device.ID] = device
		if serial, err := topologyidentity.CanonicalPhysicalIdentity(device.Serial); err == nil {
			serialCount[serial]++
		}
	}
	selected := map[string]bool{}
	out := []Device{}
	for _, id := range ids {
		if selected[id] {
			continue
		}
		selected[id] = true
		device, exists := byID[id]
		if !exists {
			return nil, errConfigurationBusy
		}
		serial, err := topologyidentity.CanonicalPhysicalIdentity(device.Serial)
		if err != nil || serialCount[serial] != 1 {
			return nil, errConfigurationStale
		}
		device.Serial = serial
		out = append(out, device)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
