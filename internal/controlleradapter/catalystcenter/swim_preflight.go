// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"errors"
	"fmt"
	"strings"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// resolveSWIMTarget requires the operator's immutable device identity to
// agree with exactly one controller inventory record before a future
// operation controller can submit a distribution or activation task.
func resolveSWIMTarget(device *ciskov1.CiscoDevice, inventory []Device) (Device, error) {
	if device == nil || device.UID == "" || device.Spec.Driver != ciskov1.DeviceDriverXE || strings.TrimSpace(device.Spec.PhysicalIdentity) == "" {
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
	if matched.ID == "" || matched.ManagementIP != device.Spec.Address {
		return Device{}, errors.New("Catalyst Center device ID or management address does not match CiscoDevice")
	}
	if !strings.EqualFold(matched.Reachability, "reachable") {
		return Device{}, fmt.Errorf("Catalyst Center reports device as %q", matched.Reachability)
	}
	return *matched, nil
}

func resolveSWIMImage(imageID string, images []Image) (Image, error) {
	if imageID == "" {
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
