// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"encoding/json"
	"sigs.k8s.io/yaml"
)

// Resolve expands the qualified upstream defaults while retaining NetAsCode
// keys. It is a source-toolchain step; native API objects never enter the CR.
func Resolve(data []byte) ([]byte, error) {
	if _, err := Compile(data); err != nil {
		return nil, err
	}
	var doc Document
	if err := yaml.UnmarshalStrict(data, &doc); err != nil {
		return nil, err
	}
	var root map[string]any
	if err := yaml.UnmarshalStrict(data, &root); err != nil {
		return nil, err
	}
	stripe := root["sdwan"].(map[string]any)
	devices := defaultBannerDevices
	if doc.Defaults != nil && doc.Defaults.SDWAN.EdgeFeatureTemplates["banner_templates"].DeviceTypes != nil {
		devices = doc.Defaults.SDWAN.EdgeFeatureTemplates["banner_templates"].DeviceTypes
	}
	if edge, ok := stripe["edge_feature_templates"].(map[string]any); ok {
		if banners, ok := edge["banner_templates"].([]any); ok {
			for _, item := range banners {
				b := item.(map[string]any)
				if _, exists := b["device_types"]; !exists {
					b["device_types"] = append([]string(nil), devices...)
				}
			}
		}
	}
	if edge, ok := stripe["edge_feature_templates"].(map[string]any); ok {
		var registry struct{ Templates map[string]classicDefinition }
		if err := json.Unmarshal(classicDefinitions, &registry); err != nil {
			return nil, err
		}
		for collection, definition := range registry.Templates {
			types := definition.DeviceTypes
			if doc.Defaults != nil && doc.Defaults.SDWAN.EdgeFeatureTemplates[collection].DeviceTypes != nil {
				types = doc.Defaults.SDWAN.EdgeFeatureTemplates[collection].DeviceTypes
			}
			items, _ := edge[collection].([]any)
			for _, item := range items {
				object := item.(map[string]any)
				if _, exists := object["device_types"]; !exists {
					object["device_types"] = append([]string(nil), types...)
				}
			}
		}
	}
	if profiles, ok := stripe["feature_profiles"].(map[string]any); ok {
		if policy, ok := profiles["policy_object_profile"].(map[string]any); ok {
			if _, exists := policy["name"]; !exists {
				policy["name"] = "policy_objects"
			}
			if _, exists := policy["description"]; !exists {
				policy["description"] = ""
			}
		}
		for profileKind, collection := range profiles {
			items, ok := collection.([]any)
			if !ok {
				continue
			}
			for _, item := range items {
				p := item.(map[string]any)
				if profileKind == "application_priority_profiles" {
					settings, ok := p["settings"].(map[string]any)
					if !ok {
						settings = map[string]any{}
						p["settings"] = settings
					}
					for _, field := range []string{"ipv4_application_visibility", "ipv6_application_visibility", "ipv4_flow_visibility", "ipv6_flow_visibility"} {
						if _, ok := settings[field]; !ok {
							settings[field] = false
						}
					}
				}
				if _, ok := p["description"]; !ok {
					p["description"] = ""
				}
				if b, ok := p["banner"].(map[string]any); ok {
					if _, ok := b["name"]; !ok {
						b["name"] = "banner"
					}
					if _, ok := b["description"]; !ok {
						b["description"] = ""
					}
				}
			}
		}
	}
	for _, groupKind := range []string{"configuration_groups", "topology_groups"} {
		groups, ok := stripe[groupKind].([]any)
		if !ok {
			continue
		}
		for _, item := range groups {
			g := item.(map[string]any)
			if _, ok := g["description"]; !ok {
				g["description"] = ""
			}
		}
	}
	if hierarchy, ok := stripe["network_hierarchy"].(map[string]any); ok {
		var resolve func(map[string]any)
		resolve = func(node map[string]any) {
			for _, kind := range []string{"groups", "regions", "sites"} {
				items, _ := node[kind].([]any)
				for _, item := range items {
					child := item.(map[string]any)
					if kind == "regions" {
						if _, ok := child["is_secondary"]; !ok {
							child["is_secondary"] = false
						}
					}
					if kind == "sites" {
						if _, ok := child["location"]; !ok {
							child["location"] = "Undisclosed"
						}
					} else {
						resolve(child)
					}
				}
			}
		}
		resolve(hierarchy)
	}
	delete(root, "defaults")
	return json.MarshalIndent(root, "", "  ")
}
