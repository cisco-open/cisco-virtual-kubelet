// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"errors"
	"net"
	"strings"
)

var errAccessPointIdentity = errors.New("access point requires a unique stable device and ethernet MAC identity")

type accessPointConfigurationTarget struct {
	device      Device
	ethernetMAC string
}

// The configure API addresses an AP by its Ethernet MAC, never its base radio
// MAC or ephemeral management IP. Keep the existing physical-identity resolver
// and require a stable serial for CVK's device reservation. This is stricter
// than the Terraform hostname fallback and avoids changing existing selectors.
func resolveAccessPointConfigurationTarget(ref configurationDeviceReference, inventory []Device) (accessPointConfigurationTarget, error) {
	device, err := resolveConfigurationDevice(ref, inventory)
	if err != nil {
		return accessPointConfigurationTarget{}, err
	}
	if ref.Serial == "" || strings.TrimSpace(device.Serial) == "" || !(device.Family == "Unified AP" || strings.HasPrefix(device.PlatformID, "C91") || strings.HasPrefix(device.PlatformID, "CW91")) {
		return accessPointConfigurationTarget{}, errAccessPointIdentity
	}
	mac, err := accessPointEthernetMAC(device.APEthernetMAC)
	if err != nil {
		return accessPointConfigurationTarget{}, err
	}
	for _, candidate := range inventory {
		if candidate.ID == device.ID {
			continue
		}
		other, err := accessPointEthernetMAC(candidate.APEthernetMAC)
		if err == nil && other == mac {
			return accessPointConfigurationTarget{}, errAccessPointIdentity
		}
	}
	return accessPointConfigurationTarget{device: device, ethernetMAC: mac}, nil
}

func accessPointEthernetMAC(value string) (string, error) {
	mac, err := net.ParseMAC(value)
	if err != nil || len(mac) != 6 || mac[0]&1 != 0 {
		return "", errAccessPointIdentity
	}
	zero := true
	for _, b := range mac {
		if b != 0 {
			zero = false
		}
	}
	if zero {
		return "", errAccessPointIdentity
	}
	return mac.String(), nil
}

func (t accessPointConfigurationTarget) member(desiredName string, manageHostname bool) (map[string]any, error) {
	if _, err := accessPointEthernetMAC(t.ethernetMAC); err != nil || !validAPIID(t.device.ID) || t.device.Serial == "" {
		return nil, errAccessPointIdentity
	}
	member := map[string]any{"macAddress": t.ethernetMAC}
	if manageHostname {
		if desiredName == "" || len(desiredName) > 255 || strings.TrimSpace(desiredName) != desiredName || strings.ContainsAny(desiredName, "\x00\r\n\t") {
			return nil, errAccessPointIdentity
		}
		member["apNameNew"] = desiredName
	}
	// apName is intentionally absent: a successful rename must not change the
	// selector on the following reconcile.
	return member, nil
}
