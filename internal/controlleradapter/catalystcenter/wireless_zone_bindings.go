// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"sort"
	"strings"
)

// Public identities only: never retain SSID credential material or copy RF
// configuration into a tag. These bindings are intended for versioned proofs;
// existing receipt MACs must not be silently reinterpreted as containing them.
type wirelessZoneBinding struct {
	Builtin bool     `json:"Builtin"`
	RF      string   `json:"RF"`
	SSIDs   []string `json:"SSIDs"`
}

// These wire names and order preserve the original v4 canonical JSON. A wire
// schema change requires a new proof version even when Go fields are renamed.
type wirelessZoneBindings struct {
	ParentID     string                         `json:"ParentID"`
	GlobalSiteID string                         `json:"GlobalSiteID"`
	Zones        map[string]wirelessZoneBinding `json:"Zones"`
	RFIDs        map[string]string              `json:"RFIDs"`
	SSIDIDs      map[string]string              `json:"SSIDIDs"`
}

// nil selects every native zone for a complete parent dependency seal. An
// explicit empty selection selects none; default-zone is an explicit built-in
// identity, not a fabricated native UUID. No mutation is performed here.
func (c *client) wirelessProfileZoneBindings(ctx context.Context, profile wirelessProfileMetadata, selected []string) (wirelessZoneBindings, error) {
	out := wirelessZoneBindings{ParentID: profile.id, Zones: map[string]wirelessZoneBinding{}, RFIDs: map[string]string{}, SSIDIDs: map[string]string{}}
	if !validAPIID(profile.id) || !validProfileName(profile.name) || profile.unmapped || len(selected) > 1000 {
		return out, errConfigurationOwned
	}
	parentSSIDs := map[string]bool{}
	if raw, exists := profile.fields["ssidDetails"]; exists {
		rows, ok := raw.([]map[string]any)
		if !ok || len(rows) > 1000 {
			return out, errInvalidResponse
		}
		for _, row := range rows {
			name, ok := row["ssidName"].(string)
			if !ok || name == "" || len(name) > 32 || strings.ContainsAny(name, "\x00\r\n") || parentSSIDs[name] {
				return out, errInvalidResponse
			}
			parentSSIDs[name] = true
		}
	}
	zones := map[string]wirelessZoneBinding{}
	if raw, exists := profile.fields["apZones"]; exists {
		rows, ok := raw.([]map[string]any)
		if !ok || len(rows) > 1000 {
			return out, errInvalidResponse
		}
		for _, row := range rows {
			for key := range row {
				if key != "apZoneName" && key != "rfProfileName" && key != "ssids" {
					return out, errConfigurationOwned
				}
			}
			name, _ := row["apZoneName"].(string)
			if !validProfileName(name) || name == "default-zone" {
				return out, errInvalidResponse
			}
			if _, exists := zones[name]; exists {
				return out, errInvalidResponse
			}
			binding := wirelessZoneBinding{SSIDs: []string{}}
			if value, exists := row["rfProfileName"]; exists {
				var ok bool
				binding.RF, ok = value.(string)
				if !ok || binding.RF != "" && !validProfileName(binding.RF) {
					return out, errInvalidResponse
				}
			}
			if value, exists := row["ssids"]; exists {
				names, ok := value.([]string)
				if !ok || len(names) > 1000 {
					return out, errInvalidResponse
				}
				seen := map[string]bool{}
				for _, name := range names {
					if name == "" || len(name) > 32 || strings.ContainsAny(name, "\x00\r\n") || seen[name] || !parentSSIDs[name] {
						return out, errInvalidResponse
					}
					seen[name] = true
					binding.SSIDs = append(binding.SSIDs, name)
				}
				sort.Strings(binding.SSIDs)
			}
			zones[name] = binding
		}
	}
	if selected == nil {
		selected = []string{}
		for name := range zones {
			selected = append(selected, name)
		}
	}
	for _, name := range selected {
		if !validProfileName(name) {
			return out, errInvalidResponse
		}
		if _, exists := out.Zones[name]; exists {
			return out, errInvalidResponse
		}
		binding, exists := zones[name]
		if name == "default-zone" {
			binding, exists = wirelessZoneBinding{Builtin: true, SSIDs: []string{}}, true
		}
		if !exists {
			return out, errConfigurationOwned
		}
		out.Zones[name] = binding
		if binding.RF != "" {
			out.RFIDs[binding.RF] = ""
		}
		for _, ssid := range binding.SSIDs {
			out.SSIDIDs[ssid] = ""
		}
	}
	if len(out.RFIDs) > 0 {
		rows, err := c.wirelessRFProfiles(ctx)
		if err != nil {
			return out, err
		}
		for name := range out.RFIDs {
			if !validAPIID(rows[name].id) {
				return out, errConfigurationOwned
			}
			out.RFIDs[name] = rows[name].id
		}
	}
	if len(out.SSIDIDs) > 0 {
		global, err := (&wirelessSSIDExecutor{client: c}).global(ctx)
		if err != nil {
			return out, err
		}
		out.GlobalSiteID = global
		rows, err := c.wirelessSSIDInventory(ctx, global)
		if err != nil {
			return out, err
		}
		for name := range out.SSIDIDs {
			row := rows[name]
			if !validAPIID(row.id) || row.sensor || row.inheritedSiteID != "" && row.inheritedSiteID != global {
				return out, errConfigurationOwned
			}
			out.SSIDIDs[name] = row.id
		}
	}
	return out, nil
}
