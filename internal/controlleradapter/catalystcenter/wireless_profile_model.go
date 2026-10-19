// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"errors"
	"sort"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

var errWirelessProfileModel = errors.New("invalid wireless profile model")

// Names remain unresolved here. The executor must resolve and bind IDs before
// submitting a profile or tag, rather than silently dropping missing references.
type wirelessProfileIntent struct {
	name                 string
	fields               map[string]any
	sites, features      []string
	ssids                []wirelessProfileSSID
	zones                []wirelessProfileZone
	siteTags, policyTags []wirelessProfileTag
}
type wirelessProfileSSID struct {
	name, dot11be string
	fields        map[string]any
}
type wirelessProfileZone struct {
	name   string
	fields map[string]any
	ssids  []string
}
type wirelessProfileTag struct {
	name         string
	fields       map[string]any
	sites, zones []string
}

func wirelessProfileObject(value any, allowed string) (map[string]any, error) {
	m, ok := value.(map[string]any)
	if !ok {
		return nil, errWirelessProfileModel
	}
	keys := map[string]bool{}
	for _, key := range strings.Split(allowed, "|") {
		keys[key] = true
	}
	for key, value := range m {
		if value == nil || !keys[key] {
			return nil, errWirelessProfileModel
		}
	}
	return m, nil
}
func wirelessProfileName(value any) (string, error) {
	s, ok := value.(string)
	if !ok || !validProfileName(s) {
		return "", errWirelessProfileModel
	}
	return s, nil
}
func wirelessProfileList(value any, sites bool) ([]string, error) {
	rows, ok := value.([]any)
	if !ok || len(rows) > 1000 {
		return nil, errWirelessProfileModel
	}
	out := []string{}
	seen := map[string]bool{}
	for _, value := range rows {
		s, err := wirelessProfileName(value)
		if err != nil || seen[s] || sites && (!validSitePath(s) || s == "Global") {
			return nil, errWirelessProfileModel
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}
func wirelessProfileRows(value any) ([]any, error) {
	rows, ok := value.([]any)
	if !ok || len(rows) > 1000 {
		return nil, errWirelessProfileModel
	}
	return rows, nil
}

func wirelessProfileSSIDNames(value any) ([]string, error) {
	rows, err := wirelessProfileRows(value)
	if err != nil {
		return nil, err
	}
	out := []string{}
	seen := map[string]bool{}
	for _, value := range rows {
		name, ok := value.(string)
		if !ok || name == "" || len(name) > 32 || strings.ContainsAny(name, "\x00\r\n") || seen[name] {
			return nil, errWirelessProfileModel
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
func parseWirelessProfiles(raw []byte) ([]wirelessProfileIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errWirelessProfileModel
	}
	section, err := wirelessProfileObject(root["network_profiles"], "wireless")
	if err != nil {
		return nil, err
	}
	rows, err := wirelessProfileRows(section["wireless"])
	if err != nil {
		return nil, errWirelessProfileModel
	}
	out := []wirelessProfileIntent{}
	seen := map[string]bool{}
	for _, row := range rows {
		m, err := wirelessProfileObject(row, "name|ssid_details|additional_interfaces|ap_zones|feature_templates|sites|site_tags|policy_tags")
		if err != nil {
			return nil, err
		}
		name, err := wirelessProfileName(m["name"])
		if err != nil || seen[name] {
			return nil, errWirelessProfileModel
		}
		seen[name] = true
		p := wirelessProfileIntent{name: name, fields: map[string]any{"wirelessProfileName": name}}
		for _, key := range []string{"additional_interfaces", "feature_templates", "sites"} {
			if value, ok := m[key]; ok {
				values, err := wirelessProfileList(value, key == "sites")
				if err != nil {
					return nil, err
				}
				switch key {
				case "sites":
					p.sites = values
				case "feature_templates":
					p.features = values
				default:
					p.fields["additionalInterfaces"] = values
				}
			}
		}
		if value, ok := m["ssid_details"]; ok {
			p.ssids, err = parseWirelessProfileSSIDs(value)
			if err != nil {
				return nil, err
			}
		}
		if value, ok := m["ap_zones"]; ok {
			p.zones, err = parseWirelessProfileZones(value)
			if err != nil {
				return nil, err
			}
		}
		if value, ok := m["site_tags"]; ok {
			p.siteTags, err = parseWirelessProfileTags(value, false)
			if err != nil {
				return nil, err
			}
		}
		if value, ok := m["policy_tags"]; ok {
			p.policyTags, err = parseWirelessProfileTags(value, true)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}
func parseWirelessProfileSSIDs(value any) ([]wirelessProfileSSID, error) {
	rows, err := wirelessProfileRows(value)
	if err != nil {
		return nil, err
	}
	out := []wirelessProfileSSID{}
	seen := map[string]bool{}
	for _, row := range rows {
		m, err := wirelessProfileObject(row, "name|enable_fabric|enable_flex_connect|local_to_vlan|interface_name|vlan_group_name|wlan_profile_name|dot11be_profile_name|anchor_group_name")
		if err != nil {
			return nil, err
		}
		name, ok := m["name"].(string)
		if !ok || name == "" || len(name) > 32 || strings.ContainsAny(name, "\x00\r\n") || seen[name] {
			return nil, errWirelessProfileModel
		}
		seen[name] = true
		s := wirelessProfileSSID{name: name, fields: map[string]any{"ssidName": name}}
		flags := map[string]bool{}
		for _, key := range []string{"enable_fabric", "enable_flex_connect"} {
			if value, exists := m[key]; exists {
				b, ok := value.(bool)
				if !ok {
					return nil, errWirelessProfileModel
				}
				flags[key] = b
				if key == "enable_fabric" {
					s.fields["enableFabric"] = b
				} else {
					s.fields["flexConnect"] = map[string]any{"enableFlexConnect": b}
				}
			}
		}
		// Match module conditionals: VLAN is emitted only with FlexConnect; fabric
		// suppresses interface/VLAN-group fields, and conflicting explicit names
		// are rejected instead of silently suppressing both.
		if value, exists := m["local_to_vlan"]; exists {
			vlan, err := wirelessAPInteger(value)
			if err != nil || vlan < 1 || vlan > 4094 {
				return nil, errWirelessProfileModel
			}
			if flags["enable_flex_connect"] {
				s.fields["flexConnect"].(map[string]any)["localToVlan"] = vlan
			}
		}
		for source, target := range map[string]string{"interface_name": "interfaceName", "vlan_group_name": "vlanGroupName", "wlan_profile_name": "wlanProfileName", "anchor_group_name": "anchorGroupName", "dot11be_profile_name": ""} {
			if value, exists := m[source]; exists {
				ref, err := wirelessProfileName(value)
				if err != nil {
					return nil, err
				}
				if source == "dot11be_profile_name" {
					s.dot11be = ref
					continue
				}
				if flags["enable_fabric"] && (source == "interface_name" || source == "vlan_group_name") {
					continue
				}
				s.fields[target] = ref
			}
		}
		_, iface := m["interface_name"]
		_, group := m["vlan_group_name"]
		if !flags["enable_fabric"] {
			if iface && group {
				return nil, errWirelessProfileModel
			}
			if !iface && !group {
				s.fields["interfaceName"] = "management"
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}
func parseWirelessProfileZones(value any) ([]wirelessProfileZone, error) {
	rows, err := wirelessProfileRows(value)
	if err != nil {
		return nil, err
	}
	out := []wirelessProfileZone{}
	seen := map[string]bool{}
	for _, row := range rows {
		m, err := wirelessProfileObject(row, "name|rf_profile_name|ssids")
		if err != nil {
			return nil, err
		}
		name, err := wirelessProfileName(m["name"])
		if err != nil || seen[name] {
			return nil, errWirelessProfileModel
		}
		seen[name] = true
		z := wirelessProfileZone{name: name, fields: map[string]any{"apZoneName": name}, ssids: []string{}}
		if value, ok := m["rf_profile_name"]; ok {
			ref, err := wirelessProfileName(value)
			if err != nil {
				return nil, err
			}
			z.fields["rfProfileName"] = ref
		}
		if value, ok := m["ssids"]; ok {
			z.ssids, err = wirelessProfileSSIDNames(value)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, z)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}
func parseWirelessProfileTags(value any, policy bool) ([]wirelessProfileTag, error) {
	rows, err := wirelessProfileRows(value)
	if err != nil {
		return nil, err
	}
	out := []wirelessProfileTag{}
	seen := map[string]bool{}
	allowed := "name|sites|ap_profile_name|flex_profile_name"
	nameKey := "siteTagName"
	if policy {
		allowed = "name|sites|ap_zones"
		nameKey = "policyTagName"
	}
	for _, row := range rows {
		m, err := wirelessProfileObject(row, allowed)
		if err != nil {
			return nil, err
		}
		name, err := wirelessProfileName(m["name"])
		if err != nil || seen[name] {
			return nil, errWirelessProfileModel
		}
		seen[name] = true
		tag := wirelessProfileTag{name: name, fields: map[string]any{nameKey: name}}
		if value, ok := m["sites"]; ok {
			tag.sites, err = wirelessProfileList(value, true)
			if err != nil {
				return nil, err
			}
		}
		if policy {
			if value, ok := m["ap_zones"]; ok {
				tag.zones, err = wirelessProfileList(value, false)
				if err != nil {
					return nil, err
				}
			}
		} else {
			tag.fields["flexProfileName"] = "default-flex-profile"
			for source, target := range map[string]string{"ap_profile_name": "apProfileName", "flex_profile_name": "flexProfileName"} {
				if value, ok := m[source]; ok {
					ref, err := wirelessProfileName(value)
					if err != nil {
						return nil, err
					}
					tag.fields[target] = ref
				}
			}
		}
		out = append(out, tag)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}
