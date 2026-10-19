// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	"net/netip"
	"net/url"
	"strings"
)

// Configuration identities use NetAsCode's inventory selectors. They do not
// replace SWIM physical-identity checks or authorize device configuration writes.
type configurationDeviceReference struct{ Name, FQDN, IP, Serial string }

func (c *client) configurationDevices(ctx context.Context) ([]Device, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	return listInventoryQueryWithGet(ctx, get, devicesPath, nil, func(d Device) string { return d.ID })
}
func resolveConfigurationDevice(ref configurationDeviceReference, inventory []Device) (Device, error) {
	if ref.Name == "" && ref.FQDN == "" && ref.IP == "" && ref.Serial == "" {
		return Device{}, errors.New("device reference is empty")
	}
	if ref.IP != "" {
		ip, err := netip.ParseAddr(ref.IP)
		if err != nil || ip.Zone() != "" {
			return Device{}, errors.New("device reference requires a literal management IP")
		}
	}
	ids := map[string]bool{}
	var selected *Device
	for i := range inventory {
		d := &inventory[i]
		if !validAPIID(d.ID) || ids[d.ID] {
			return Device{}, errInvalidResponse
		}
		ids[d.ID] = true
		ap := strings.HasPrefix(d.PlatformID, "C91") || strings.HasPrefix(d.PlatformID, "CW91")
		named := (ref.Name != "" && ref.Name == d.Hostname) || (ref.FQDN != "" && ref.FQDN == d.Hostname)
		serial := ref.Serial != "" && strings.EqualFold(strings.TrimSpace(ref.Serial), strings.TrimSpace(d.Serial))
		address := ref.IP != "" && !ap && sameManagementIP(ref.IP, d.ManagementIP)
		if !named && !serial && !address {
			continue
		}
		if selected != nil {
			return Device{}, errors.New("device selectors resolve ambiguously")
		}
		selected = d
	}
	if selected == nil {
		return Device{}, errors.New("device reference is absent from controller inventory")
	}
	// Names can be local model aliases; supplied physical selectors must agree.
	if ref.Serial != "" && !strings.EqualFold(strings.TrimSpace(ref.Serial), strings.TrimSpace(selected.Serial)) {
		return Device{}, errors.New("device serial conflicts with controller inventory")
	}
	ap := strings.HasPrefix(selected.PlatformID, "C91") || strings.HasPrefix(selected.PlatformID, "CW91")
	if ref.IP != "" && !ap && !sameManagementIP(ref.IP, selected.ManagementIP) {
		return Device{}, errors.New("device management IP conflicts with controller inventory")
	}
	if ap && ref.Serial == "" {
		return Device{}, errors.New("access point configuration requires its stable serial identity")
	}
	return *selected, nil
}
