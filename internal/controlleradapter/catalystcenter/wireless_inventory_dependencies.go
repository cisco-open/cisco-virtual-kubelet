// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"strings"
)

// Request-local inventory for prospective wireless stage validation. This
// preserves explicit profile assignments; it does not guess inherited policy.
type wirelessInventoryDependencies struct {
	devices      []Device
	sites        map[string]Site
	profiles     map[string]wirelessProfileMetadata
	profileSites map[string]map[string]bool
}

func (c *client) wirelessInventoryDependencies(ctx context.Context) (wirelessInventoryDependencies, error) {
	out := wirelessInventoryDependencies{sites: map[string]Site{}, profileSites: map[string]map[string]bool{}}
	var err error
	out.devices, err = c.configurationDevices(ctx)
	if err != nil {
		return out, err
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return out, err
	}
	ids := map[string]bool{}
	for _, site := range sites {
		if !validAPIID(site.ID) || !validSitePath(site.NameHierarchy) || ids[site.ID] {
			return out, errInvalidResponse
		}
		if _, exists := out.sites[site.NameHierarchy]; exists {
			return out, errInvalidResponse
		}
		ids[site.ID] = true
		out.sites[site.NameHierarchy] = site
	}
	out.profiles, err = c.wirelessProfileInventory(ctx)
	if err != nil {
		return out, err
	}
	for _, profile := range out.profiles {
		assigned, err := c.profileAssignedSites(ctx, profile.id)
		if err != nil {
			return out, err
		}
		for id := range assigned {
			if !ids[id] {
				return out, errConfigurationStale
			}
		}
		out.profileSites[profile.id] = assigned
	}
	return out, nil
}

// Corroborate one explicitly assigned standalone WLC placement. Fabric and
// inherited profile selection require their own validated dependency resolver.
// The result binds every used profile and site without performing a mutation.
func (s wirelessInventoryDependencies) standalonePlacement(desired inventoryDeviceIntent) (Device, map[string]string, error) {
	var empty Device
	if desired.wireless == nil || desired.reference.Serial == "" || desired.wireless.deviceType != "WirelessController" || len(desired.wireless.roles) != 0 || desired.wireless.fabricSite != "" {
		return empty, nil, errConfigurationTransition
	}
	device, err := resolveConfigurationDevice(desired.reference, s.devices)
	if err != nil {
		return empty, nil, err
	}
	if device.Family != "Wireless Controller" || device.Reachability != "Reachable" {
		return empty, nil, errConfigurationTransition
	}
	site, exists := s.sites[desired.site]
	if !exists || site.Type == "global" || !validAPIID(site.ID) {
		return empty, nil, errConfigurationStale
	}
	bindings := map[string]string{"device": device.ID, "serial": device.Serial, "address": device.ManagementIP, "site:" + desired.site: site.ID}
	locations := desired.wireless.locations
	for _, paths := range [][]string{locations.primary, locations.secondary, locations.anchor} {
		for _, path := range paths {
			location, exists := s.sites[path]
			if !exists || location.Type == "global" || !validAPIID(location.ID) {
				return empty, nil, errConfigurationStale
			}
			bindings["site:"+path] = location.ID
			matches := 0
			for name, profile := range s.profiles {
				if !s.profileSites[profile.id][location.ID] {
					continue
				}
				if profile.unmapped || !validAPIID(profile.id) {
					return empty, nil, errConfigurationOwned
				}
				matches++
				bindings["profile:"+name] = profile.id
			}
			if matches == 0 {
				return empty, nil, errConfigurationBusy
			}
			if matches > 1 {
				return empty, nil, errConfigurationOwned
			}
		}
	}
	return device, bindings, nil
}

// Validate only selected profiles using the same preserved-field dependency
// resolution as profile updates. Include content and referenced UUIDs in the
// placement binding so revalidation detects more than a profile UUID change.
func (c *client) validateWirelessPlacementProfiles(ctx context.Context, snapshot wirelessInventoryDependencies, placement map[string]string) (map[string]string, error) {
	bindings := make(map[string]string, len(placement))
	for key, value := range placement {
		bindings[key] = value
	}
	for key, id := range placement {
		if !strings.HasPrefix(key, "profile:") {
			continue
		}
		name := strings.TrimPrefix(key, "profile:")
		actual, exists := snapshot.profiles[name]
		if !exists || actual.id != id || actual.unmapped {
			return nil, errConfigurationStale
		}
		want := wirelessProfileIntent{name: name}
		references, err := c.wirelessProfileReferences(ctx, want, &actual)
		if err != nil {
			return nil, err
		}
		payload, err := want.payload(references, &actual)
		if err != nil {
			return nil, err
		}
		referenced, err := wirelessProfileBinding(payload, references)
		if err != nil {
			return nil, err
		}
		digest, err := wirelessTagDigest(map[string]any{"profile": id, "payload": payload, "references": referenced})
		if err != nil {
			return nil, err
		}
		bindings["profile-content:"+name] = digest
	}
	return bindings, nil
}

// Validate the complete set before the staged executor selects a prerequisite
// write. Shared profile content is checked once per request, and two model
// entries may not accidentally address the same physical inventory object.
func (c *client) validateStandaloneWirelessInventory(ctx context.Context, desired []inventoryDeviceIntent) (map[string]string, error) {
	snapshot, err := c.wirelessInventoryDependencies(ctx)
	if err != nil {
		return nil, err
	}
	return c.validateStandaloneWirelessSnapshot(ctx, snapshot, desired)
}

func (c *client) validateStandaloneWirelessSnapshot(ctx context.Context, snapshot wirelessInventoryDependencies, desired []inventoryDeviceIntent) (map[string]string, error) {
	bindings := map[string]string{}
	selectedProfiles := map[string]string{}
	seen := map[string]bool{}
	for _, intent := range desired {
		if intent.wireless == nil {
			continue
		}
		device, placement, err := snapshot.standalonePlacement(intent)
		if err != nil {
			return nil, err
		}
		if seen[device.ID] {
			return nil, errConfigurationOwned
		}
		seen[device.ID] = true
		for key, value := range placement {
			if strings.HasPrefix(key, "profile:") {
				selectedProfiles[key] = value
			}
		}
		digest, err := wirelessTagDigest(placement)
		if err != nil {
			return nil, err
		}
		bindings["placement:"+intent.reference.Name] = digest
	}
	profiles, err := c.validateWirelessPlacementProfiles(ctx, snapshot, selectedProfiles)
	if err != nil {
		return nil, err
	}
	for key, value := range profiles {
		bindings[key] = value
	}
	return bindings, nil
}
