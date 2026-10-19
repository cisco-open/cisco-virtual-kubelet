// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"sort"
	"strings"
)

type fabricSSIDProfileBinding struct {
	Name, ProfileID, ProfileName, SiteID, SitePath, SSIDID, InheritedSiteID string
}

// Resolve explicit profile associations within the fabric site subtree. This
// proves design eligibility, not WLC provisioning or inherited profile selection.
func (c *client) fabricVLANSSIDProfiles(ctx context.Context, path string, mappings []fabricVLANSSID) ([]fabricSSIDProfileBinding, error) {
	rows, err := canonicalFabricVLANSSIDs(mappings)
	if err != nil {
		return nil, err
	}
	required := map[string]bool{}
	for _, row := range rows {
		for _, ssid := range row.SSIDs {
			required[ssid.Name] = true
		}
	}
	snapshot, err := c.wirelessInventoryDependencies(ctx)
	if err != nil {
		return nil, err
	}
	return resolveFabricSSIDProfiles(path, required, snapshot, func(siteID string) (map[string]wirelessSSIDMetadata, error) {
		return c.wirelessSSIDInventory(ctx, siteID)
	})
}

func resolveFabricSSIDProfiles(path string, required map[string]bool, snapshot wirelessInventoryDependencies, definitions func(string) (map[string]wirelessSSIDMetadata, error)) ([]fabricSSIDProfileBinding, error) {
	if !validSitePath(path) || path == "Global" || definitions == nil {
		return nil, errInvalidResponse
	}
	root, exists := snapshot.sites[path]
	if !exists || !validAPIID(root.ID) {
		return nil, errConfigurationBusy
	}
	paths := []string{}
	for candidate := range snapshot.sites {
		if candidate == path || strings.HasPrefix(candidate, path+"/") {
			paths = append(paths, candidate)
		}
	}
	sort.Strings(paths)
	profiles := []string{}
	for name := range snapshot.profiles {
		profiles = append(profiles, name)
	}
	sort.Strings(profiles)
	found := map[string]bool{}
	out := []fabricSSIDProfileBinding{}
	for _, sitePath := range paths {
		site := snapshot.sites[sitePath]
		if !validAPIID(site.ID) {
			return nil, errInvalidResponse
		}
		var inventory map[string]wirelessSSIDMetadata
		seen := map[string]bool{}
		for _, name := range profiles {
			profile := snapshot.profiles[name]
			if !snapshot.profileSites[profile.id][site.ID] {
				continue
			}
			entries, _ := profile.fields["ssidDetails"].([]map[string]any)
			for _, entry := range entries {
				ssid, _ := entry["ssidName"].(string)
				if !required[ssid] {
					continue
				}
				if seen[ssid] || profile.unmapped || !validAPIID(profile.id) || entry["enableFabric"] != true {
					return nil, errConfigurationStale
				}
				if flex, ok := entry["flexConnect"].(map[string]any); ok && flex["enableFlexConnect"] == true {
					return nil, errConfigurationStale
				}
				seen[ssid] = true
				if inventory == nil {
					var err error
					inventory, err = definitions(site.ID)
					if err != nil {
						return nil, err
					}
				}
				definition, exists := inventory[ssid]
				if !exists || !validAPIID(definition.id) || definition.name != ssid || definition.sensor {
					return nil, errConfigurationBusy
				}
				out = append(out, fabricSSIDProfileBinding{Name: ssid, ProfileID: profile.id, ProfileName: name, SiteID: site.ID, SitePath: sitePath, SSIDID: definition.id, InheritedSiteID: definition.inheritedSiteID})
				found[ssid] = true
			}
		}
	}
	for name := range required {
		if !found[name] {
			return nil, errConfigurationBusy
		}
	}
	return out, nil
}
