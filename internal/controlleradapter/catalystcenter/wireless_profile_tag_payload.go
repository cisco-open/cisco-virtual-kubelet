// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"errors"
	"sort"
)

var errWirelessSiteTagAPProfile = errors.New("wireless site tag requires an explicit or retained ap_profile_name")

// Identities originate from authoritative readers and are bound to operations.
type wirelessProfileTagReferences struct {
	profileID                string
	sites                    map[string]string
	assigned                 map[string]bool
	zones                    map[string]bool
	apProfiles, flexProfiles map[string]string
}

func (want wirelessProfileTag) payload(refs wirelessProfileTagReferences, actual *wirelessProfileTagMetadata, policy bool) (map[string]any, error) {
	if !validAPIID(refs.profileID) {
		return nil, errWirelessProfileModel
	}
	return want.dependencyPayload(refs, actual, policy)
}

// dependencyPayload also validates prospective dependencies before a parent exists.
// It never produces an executable operation or invents an appliance identity.
func (want wirelessProfileTag) dependencyPayload(refs wirelessProfileTagReferences, actual *wirelessProfileTagMetadata, policy bool) (map[string]any, error) {
	if len(want.sites) == 0 {
		return nil, errWirelessProfileModel
	}
	nameKey := "siteTagName"
	if policy {
		nameKey = "policyTagName"
	}
	if want.fields[nameKey] != want.name || !validProfileName(want.name) {
		return nil, errWirelessProfileModel
	}
	base := map[string]any{}
	if actual != nil {
		if actual.unmapped || actual.profileID != refs.profileID || actual.name != want.name || !validAPIID(actual.id) {
			return nil, errConfigurationOwned
		}
		base = actual.fields
	}
	out := mergeWirelessProfileFields(base, want.fields)
	ids := []string{}
	seen := map[string]bool{}
	for _, path := range want.sites {
		id := refs.sites[path]
		if !validSitePath(path) || path == "Global" || !validAPIID(id) || seen[id] || !refs.assigned[id] {
			return nil, errWirelessProfileModel
		}
		ids = append(ids, id)
		seen[id] = true
	}
	sort.Strings(ids)
	out["siteIds"] = ids
	if policy {
		if want.zones != nil {
			out["apZones"] = append([]string{}, want.zones...)
		}
		if zones, exists := out["apZones"]; !exists || emptyWirelessPolicyZones(zones) {
			out["apZones"] = []string{"default-zone"}
		}
		zones, ok := out["apZones"].([]string)
		if !ok {
			return nil, errInvalidResponse
		}
		seen := map[string]bool{}
		for _, zone := range zones {
			if !validProfileName(zone) || seen[zone] || !refs.zones[zone] {
				return nil, errWirelessProfileModel
			}
			seen[zone] = true
		}
		sort.Strings(zones)
	} else {
		// Native create and update both require this field. Preserve a known
		// observed value when omitted; do not invent a default AP reference.
		if name, ok := out["apProfileName"].(string); !ok || name == "" {
			return nil, errWirelessSiteTagAPProfile
		}
		for field, index := range map[string]map[string]string{"apProfileName": refs.apProfiles, "flexProfileName": refs.flexProfiles} {
			if value, exists := out[field]; exists {
				name, ok := value.(string)
				if !ok {
					return nil, errInvalidResponse
				}
				// The pinned model names the built-in default directly; do not
				// manufacture an inventory UUID for a built-in name.
				if field == "flexProfileName" && name == "default-flex-profile" {
					continue
				}
				if name != "" && !wirelessProfileReference(index, name) {
					return nil, errWirelessProfileModel
				}
			}
		}
	}
	return out, nil
}

func emptyWirelessPolicyZones(value any) bool {
	zones, ok := value.([]string)
	return ok && len(zones) == 0
}
