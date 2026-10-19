// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"net/netip"
	"strings"
)

type managementAddressChange struct {
	device        Device
	before, after string
}

// device_ip is the requested new controller address for this operation, not an
// additional current-address selector. Resolve the existing native UUID by
// name/FQDN and corroborating serial, then explicitly check IP ownership.
// This planner does not change the device interface or authorize an API write.
func planManagementAddressChange(ref configurationDeviceReference, inventory []Device) (*managementAddressChange, error) {
	if ref.Name == "" && ref.FQDN == "" {
		return nil, errInvalidResponse
	}
	wanted, err := netip.ParseAddr(ref.IP)
	if err != nil || wanted.Zone() != "" || wanted.Is4In6() || !wanted.IsGlobalUnicast() {
		return nil, errInvalidResponse
	}
	identity := ref
	identity.IP = ""
	selected, err := resolveConfigurationDevice(identity, inventory)
	if err != nil {
		return nil, err
	}
	if selected.Serial == "" || strings.TrimSpace(selected.Serial) != selected.Serial || strings.ContainsAny(selected.Serial, "\x00\r\n") {
		return nil, errConfigurationDeviceAdmission
	}
	if selected.APEthernetMAC != "" || strings.EqualFold(selected.Family, "Unified AP") || strings.HasPrefix(selected.PlatformID, "C91") || strings.HasPrefix(selected.PlatformID, "CW91") || strings.HasPrefix(selected.PlatformID, "AIR-AP") || strings.HasPrefix(selected.PlatformID, "AIR-CAP") {
		return nil, errConfigurationDeviceAdmission
	}
	before, err := netip.ParseAddr(selected.ManagementIP)
	if err != nil || before.Zone() != "" || before.Is4In6() || !before.IsGlobalUnicast() {
		return nil, errInvalidResponse
	}
	for _, device := range inventory {
		if device.ID == selected.ID || device.ManagementIP == "" {
			continue
		}
		address, err := netip.ParseAddr(device.ManagementIP)
		if err != nil || address.Zone() != "" {
			return nil, errInvalidResponse
		}
		if address.Unmap() == wanted {
			return nil, errConfigurationDeviceAdmission
		}
	}
	if before == wanted {
		return nil, nil
	}
	return &managementAddressChange{device: selected, before: before.String(), after: wanted.String()}, nil
}

func (c *client) managementAddressCandidate(ctx context.Context, ref configurationDeviceReference) (*managementAddressChange, error) {
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	return planManagementAddressChange(ref, inventory)
}
