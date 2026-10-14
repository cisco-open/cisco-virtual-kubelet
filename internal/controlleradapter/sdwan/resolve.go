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
	if doc.Defaults != nil && doc.Defaults.SDWAN.EdgeFeatureTemplates.BannerTemplates.DeviceTypes != nil {
		devices = doc.Defaults.SDWAN.EdgeFeatureTemplates.BannerTemplates.DeviceTypes
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
	if profiles, ok := stripe["feature_profiles"].(map[string]any); ok {
		for _, collection := range profiles {
			items, ok := collection.([]any)
			if !ok {
				continue
			}
			for _, item := range items {
				p := item.(map[string]any)
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
	if groups, ok := stripe["configuration_groups"].([]any); ok {
		for _, item := range groups {
			g := item.(map[string]any)
			if _, ok := g["description"]; !ok {
				g["description"] = ""
			}
		}
	}
	delete(root, "defaults")
	return json.MarshalIndent(root, "", "  ")
}
