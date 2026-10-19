// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"reflect"
	"sort"
	"strings"
)

// Ownership must be supplied from a verified application receipt by the caller,
// scoped to this source and fabric. A matching pre-existing row is not adopted.
type fabricVLANSSIDOwned struct {
	VLAN          string
	SecurityGroup *string
}
type fabricVLANSSIDSelection struct {
	payload      []fabricVLANSSID
	added, moved []string
}

func canonicalFabricVLANSSIDs(rows []fabricVLANSSID) ([]fabricVLANSSID, error) {
	if len(rows) > 5000 {
		return nil, errInvalidResponse
	}
	out := make([]fabricVLANSSID, 0, len(rows))
	vlans, names := map[string]bool{}, map[string]bool{}
	for _, row := range rows {
		if !validProfileName(row.VLAN) || vlans[row.VLAN] || row.SSIDs == nil || len(row.SSIDs) > 1000 {
			return nil, errInvalidResponse
		}
		vlans[row.VLAN] = true
		copy := fabricVLANSSID{VLAN: row.VLAN, SSIDs: make([]fabricSSIDBinding, 0, len(row.SSIDs))}
		for _, ssid := range row.SSIDs {
			if ssid.Name == "" || len(ssid.Name) > 32 || strings.ContainsAny(ssid.Name, "\x00\r\n") || names[ssid.Name] {
				return nil, errInvalidResponse
			}
			names[ssid.Name] = true
			if ssid.SecurityGroup != nil {
				tag := *ssid.SecurityGroup
				if tag != "" && !validProfileName(tag) {
					return nil, errInvalidResponse
				}
				ssid.SecurityGroup = &tag
			}
			copy.SSIDs = append(copy.SSIDs, ssid)
		}
		sort.Slice(copy.SSIDs, func(i, j int) bool { return copy.SSIDs[i].Name < copy.SSIDs[j].Name })
		out = append(out, copy)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VLAN < out[j].VLAN })
	return out, nil
}

// The native PUT replaces the full fabric mapping set. Preserve all undeclared
// entries, including empty VLAN lists and policy tags. No implicit deletion is
// authorized by omission, and moving an unowned or externally changed SSID fails.
func selectFabricVLANSSIDs(want, before []fabricVLANSSID, owned map[string]fabricVLANSSIDOwned) (*fabricVLANSSIDSelection, error) {
	desired, err := canonicalFabricVLANSSIDs(want)
	if err != nil {
		return nil, err
	}
	observed, err := canonicalFabricVLANSSIDs(before)
	if err != nil {
		return nil, err
	}
	vlans := map[string][]fabricSSIDBinding{}
	location := map[string]string{}
	bindings := map[string]fabricSSIDBinding{}
	for _, row := range observed {
		vlans[row.VLAN] = row.SSIDs
		for _, ssid := range row.SSIDs {
			location[ssid.Name] = row.VLAN
			bindings[ssid.Name] = ssid
		}
	}
	out := &fabricVLANSSIDSelection{}
	for _, row := range desired {
		for _, ssid := range row.SSIDs {
			if ssid.SecurityGroup != nil {
				return nil, errInvalidResponse
			}
			previous, exists := location[ssid.Name]
			if exists && previous == row.VLAN {
				continue
			}
			if exists {
				evidence, ok := owned[ssid.Name]
				binding := bindings[ssid.Name]
				if !ok || evidence.VLAN != previous || !reflect.DeepEqual(evidence.SecurityGroup, binding.SecurityGroup) {
					return nil, errConfigurationStale
				}
				retained := make([]fabricSSIDBinding, 0, len(vlans[previous]))
				for _, old := range vlans[previous] {
					if old.Name != ssid.Name {
						retained = append(retained, old)
					}
				}
				vlans[previous] = retained
				ssid = binding
				out.moved = append(out.moved, ssid.Name)
			} else {
				out.added = append(out.added, ssid.Name)
			}
			vlans[row.VLAN] = append(vlans[row.VLAN], ssid)
		}
	}
	merged := make([]fabricVLANSSID, 0, len(vlans))
	for vlan, ssids := range vlans {
		merged = append(merged, fabricVLANSSID{VLAN: vlan, SSIDs: ssids})
	}
	out.payload, err = canonicalFabricVLANSSIDs(merged)
	if err != nil {
		return nil, err
	}
	sort.Strings(out.added)
	sort.Strings(out.moved)
	return out, nil
}

// Journal one changed SSID per PUT. Native replacement still preserves all
// other rows, but the receipt can identify exactly which binding CVK changed.
// Validate the complete declared set first, so a later conflict cannot hide
// behind an earlier writable entry.
func selectNextFabricVLANSSID(want, before []fabricVLANSSID, owned map[string]fabricVLANSSIDOwned) (*fabricVLANSSIDSelection, *fabricVLANSSID, error) {
	all, err := selectFabricVLANSSIDs(want, before, owned)
	if err != nil {
		return nil, nil, err
	}
	pending := append(append([]string{}, all.added...), all.moved...)
	sort.Strings(pending)
	if len(pending) == 0 {
		return all, nil, nil
	}
	name := pending[0]
	for _, row := range want {
		for _, ssid := range row.SSIDs {
			if ssid.Name != name {
				continue
			}
			selected, err := selectFabricVLANSSIDs([]fabricVLANSSID{{VLAN: row.VLAN, SSIDs: []fabricSSIDBinding{ssid}}}, before, owned)
			if err != nil {
				return nil, nil, err
			}
			for _, actual := range selected.payload {
				for _, binding := range actual.SSIDs {
					if binding.Name == name {
						return selected, &fabricVLANSSID{VLAN: actual.VLAN, SSIDs: []fabricSSIDBinding{binding}}, nil
					}
				}
			}
		}
	}
	return nil, nil, errInvalidResponse
}
