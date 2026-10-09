// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"errors"
	"net/netip"
	"strings"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// resolveSWIMTarget requires the operator's immutable device identity to
// agree with exactly one controller inventory record before a future
// operation controller can proceed to admission. This lookup is not mutation
// authorization: topology, image compatibility, claims and leases are separate.
func resolveSWIMTarget(device *ciskov1.CiscoDevice, inventory []Device) (Device, error) {
	if device == nil || device.Name == "" || device.Namespace == "" || device.UID == "" || !device.DeletionTimestamp.IsZero() || device.Spec.Driver != ciskov1.DeviceDriverXE || strings.TrimSpace(device.Spec.PhysicalIdentity) == "" {
		return Device{}, errors.New("SWIM requires a persisted IOS XE CiscoDevice with physicalIdentity")
	}
	var matched *Device
	for i := range inventory {
		item := &inventory[i]
		if !strings.EqualFold(strings.TrimSpace(item.Serial), strings.TrimSpace(device.Spec.PhysicalIdentity)) {
			continue
		}
		if matched != nil {
			return Device{}, errors.New("Catalyst Center inventory has duplicate device serials")
		}
		matched = item
	}
	if matched == nil {
		return Device{}, errors.New("device serial is absent from Catalyst Center inventory")
	}
	if !validAPIID(matched.ID) || !sameManagementIP(matched.ManagementIP, device.Spec.Address) {
		return Device{}, errors.New("Catalyst Center device ID or management address does not match CiscoDevice")
	}
	for i := range inventory {
		if &inventory[i] != matched && (inventory[i].ID == matched.ID || sameManagementIP(inventory[i].ManagementIP, matched.ManagementIP)) {
			return Device{}, errors.New("Catalyst Center inventory has an ambiguous device ID or management address")
		}
	}
	if !strings.EqualFold(matched.Reachability, "reachable") {
		return Device{}, errors.New("Catalyst Center does not report the device as reachable")
	}
	return *matched, nil
}

// Do not use DNS to prove physical identity. A hostname requires a separately
// verified inventory binding; this initial preflight accepts literal IPs only.
func sameManagementIP(a, b string) bool {
	x, err := netip.ParseAddr(a)
	if err != nil {
		return false
	}
	y, err := netip.ParseAddr(b)
	return err == nil && x.Unmap() == y.Unmap()
}

func resolveSWIMImage(imageID string, images []Image) (Image, error) {
	if !validAPIID(imageID) {
		return Image{}, errors.New("SWIM image UUID is required")
	}
	var matched *Image
	for i := range images {
		if images[i].ID != imageID {
			continue
		}
		if matched != nil {
			return Image{}, errors.New("Catalyst Center inventory has duplicate image UUIDs")
		}
		matched = &images[i]
	}
	if matched == nil {
		return Image{}, errors.New("SWIM image is absent from Catalyst Center inventory")
	}
	return *matched, nil
}
