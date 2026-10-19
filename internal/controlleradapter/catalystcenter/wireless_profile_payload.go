// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"errors"
	"sort"
)

var errWirelessAPZoneCombination = errors.New("SSID and RF profile combination must be unique across AP zones")
var errWirelessProfileSwitchingModes = errors.New("one wireless profile cannot combine Fabric and FlexConnect SSIDs")

func validateWirelessProfileSwitchingModes(fields map[string]any) error {
	fabric, flex := false, false
	entries, _ := fields["ssidDetails"].([]map[string]any)
	for _, entry := range entries {
		fabric = fabric || entry["enableFabric"] == true
		if settings, ok := entry["flexConnect"].(map[string]any); ok {
			flex = flex || settings["enableFlexConnect"] == true
		}
	}
	if fabric && flex {
		return errWirelessProfileSwitchingModes
	}
	return nil
}

// A request-local, validated inventory snapshot. Names that collide across
// feature-template kinds must be rejected by the collector, not overwritten.
type wirelessProfileReferences struct {
	global                                     string
	ssids                                      map[string]wirelessSSIDMetadata
	interfaces, dot11be, anchors, rf, features map[string]string
}

func wirelessProfileReference(refs map[string]string, name string) bool {
	return name != "" && validAPIID(refs[name])
}
func wirelessProfileReferenceID(refs map[string]string, id string) bool {
	if !validAPIID(id) {
		return false
	}
	for _, candidate := range refs {
		if candidate == id {
			return true
		}
	}
	return false
}
func cloneWirelessProfileValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, value := range v {
			out[key] = cloneWirelessProfileValue(value)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(v))
		for i, value := range v {
			out[i] = cloneWirelessProfileValue(value).(map[string]any)
		}
		return out
	case []string:
		return append([]string{}, v...)
	default:
		return value
	}
}
func mergeWirelessProfileFields(base, desired map[string]any) map[string]any {
	out := cloneWirelessProfileValue(base).(map[string]any)
	for key, value := range desired {
		if object, ok := value.(map[string]any); ok {
			old, _ := out[key].(map[string]any)
			out[key] = mergeWirelessProfileFields(old, object)
		} else {
			out[key] = cloneWirelessProfileValue(value)
		}
	}
	return out
}
func wirelessProfilePriorEntry(fields map[string]any, collection, key, name string) map[string]any {
	entries, _ := fields[collection].([]map[string]any)
	for _, entry := range entries {
		if entry[key] == name {
			return entry
		}
	}
	return nil
}

func mergeWirelessProfileSSID(old map[string]any, ssid wirelessProfileSSID) map[string]any {
	entry := mergeWirelessProfileFields(old, ssid.fields)
	if entry["enableFabric"] == true {
		delete(entry, "interfaceName")
		delete(entry, "vlanGroupName")
	} else {
		if _, owned := ssid.fields["interfaceName"]; owned {
			delete(entry, "vlanGroupName")
		}
		if _, owned := ssid.fields["vlanGroupName"]; owned {
			delete(entry, "interfaceName")
		}
	}
	if flex, ok := entry["flexConnect"].(map[string]any); ok && flex["enableFlexConnect"] == false {
		delete(flex, "localToVlan")
	}
	return entry
}

// Construct the complete public object while preserving unowned known fields.
// Missing/ambiguous reference collection is an error before this boundary.
func (want wirelessProfileIntent) payload(refs wirelessProfileReferences, actual *wirelessProfileMetadata) (map[string]any, error) {
	if !validAPIID(refs.global) {
		return nil, errWirelessProfileModel
	}
	base := map[string]any{}
	if actual != nil {
		if actual.unmapped || actual.name != want.name || !validAPIID(actual.id) {
			return nil, errConfigurationOwned
		}
		base = actual.fields
	}
	out := mergeWirelessProfileFields(base, want.fields)
	if want.ssids != nil {
		entries := []map[string]any{}
		for _, ssid := range want.ssids {
			old := wirelessProfilePriorEntry(base, "ssidDetails", "ssidName", ssid.name)
			entry := mergeWirelessProfileSSID(old, ssid)
			if ssid.dot11be != "" {
				if !wirelessProfileReference(refs.dot11be, ssid.dot11be) {
					return nil, errWirelessProfileModel
				}
				entry["dot11beProfileId"] = refs.dot11be[ssid.dot11be]
			}

			entries = append(entries, entry)
		}
		out["ssidDetails"] = entries
	}
	if want.zones != nil {
		entries := []map[string]any{}
		for _, zone := range want.zones {
			entry := mergeWirelessProfileFields(wirelessProfilePriorEntry(base, "apZones", "apZoneName", zone.name), zone.fields)
			entry["ssids"] = append([]string{}, zone.ssids...)
			entries = append(entries, entry)
		}
		out["apZones"] = entries
	}
	if want.features != nil {
		entries := []map[string]any{}
		for _, name := range want.features {
			if !wirelessProfileReference(refs.features, name) {
				return nil, errWirelessProfileModel
			}
			entries = append(entries, map[string]any{"id": refs.features[name], "ssids": []string{}})
		}
		out["featureTemplates"] = entries
	}
	if err := validateWirelessProfileSwitchingModes(out); err != nil {
		return nil, err
	}
	// Validate the merged object, including dependencies retained from prior
	// state. Validating only explicit intent can leave dangling old references.
	ssids := map[string]bool{}
	entries, _ := out["ssidDetails"].([]map[string]any)
	for _, entry := range entries {
		name, _ := entry["ssidName"].(string)
		actual, exists := refs.ssids[name]
		if !exists || !validAPIID(actual.id) || actual.sensor || actual.inheritedSiteID != "" && actual.inheritedSiteID != refs.global || ssids[name] {
			return nil, errWirelessProfileModel
		}
		ssids[name] = true
		// This field is a named design selector, not a globally inventoried
		// object UUID. Provisioning must separately qualify controller realization.
		if value, exists := entry["vlanGroupName"]; exists {
			name, ok := value.(string)
			if !ok || name != "" && !validProfileName(name) {
				return nil, errWirelessProfileModel
			}
		}
		for field, index := range map[string]map[string]string{"interfaceName": refs.interfaces, "anchorGroupName": refs.anchors} {
			if name, _ := entry[field].(string); name != "" && !wirelessProfileReference(index, name) {
				return nil, errWirelessProfileModel
			}
		}
		if id, _ := entry["dot11beProfileId"].(string); id != "" && !wirelessProfileReferenceID(refs.dot11be, id) {
			return nil, errWirelessProfileModel
		}
	}
	interfaces, _ := out["additionalInterfaces"].([]string)
	for _, name := range interfaces {
		if !wirelessProfileReference(refs.interfaces, name) {
			return nil, errWirelessProfileModel
		}
	}
	zones, _ := out["apZones"].([]map[string]any)
	zonePairs := map[[2]string]bool{}
	for _, zone := range zones {
		if name, _ := zone["rfProfileName"].(string); name != "" && !wirelessProfileReference(refs.rf, name) {
			return nil, errWirelessProfileModel
		}
		names, _ := zone["ssids"].([]string)
		for _, name := range names {
			if !ssids[name] {
				return nil, errWirelessProfileModel
			}
			rf, _ := zone["rfProfileName"].(string)
			pair := [2]string{name, rf}
			if zonePairs[pair] {
				return nil, errWirelessAPZoneCombination
			}
			zonePairs[pair] = true
		}
	}
	features, _ := out["featureTemplates"].([]map[string]any)
	for _, feature := range features {
		id, _ := feature["id"].(string)
		if !wirelessProfileReferenceID(refs.features, id) {
			return nil, errWirelessProfileModel
		}
		names, _ := feature["ssids"].([]string)
		for _, name := range names {
			if !ssids[name] {
				return nil, errWirelessProfileModel
			}
		}
	}
	for collection, key := range map[string]string{"ssidDetails": "ssidName", "apZones": "apZoneName", "featureTemplates": "id"} {
		if rows, ok := out[collection].([]map[string]any); ok {
			sort.Slice(rows, func(i, j int) bool { return rows[i][key].(string) < rows[j][key].(string) })
		}
	}
	return out, nil
}
