// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"errors"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

// Wireless feature collections use the same aggregate executor/report wrapper
// as wireless definitions, while retaining their independent section owner.
func (c *client) compileWirelessFeatureConfiguration(raw []byte) (*wirelessConfiguration, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errors.New("feature_templates requires a section-specific model")
	}
	section, ok := root["feature_templates"].(map[string]any)
	if !ok || len(section) != 1 {
		return nil, errors.New("unsupported feature template domain")
	}
	wireless, ok := section["wireless"].(map[string]any)
	if !ok || len(wireless) == 0 {
		return nil, errors.New("invalid wireless feature collections")
	}
	for key := range wireless {
		if key != "rrm_fra" && key != "cleanair" {
			return nil, errors.New("unsupported wireless feature collection")
		}
	}
	out := &wirelessConfiguration{}
	parts := []configurationPart{}
	for _, key := range []string{"rrm_fra", "cleanair"} {
		value, ok := wireless[key]
		if !ok {
			continue
		}
		projection, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"feature_templates": map[string]any{"wireless": map[string]any{key: value}}}})
		switch key {
		case "rrm_fra":
			desired, err := parseWirelessFRA(projection)
			if err != nil {
				return nil, err
			}
			e := &wirelessFRAExecutor{client: c, desired: desired}
			parts = append(parts, configurationPart{name: key, executor: e})
			out.reports = append(out.reports, e.report)
		case "cleanair":
			desired, err := parseWirelessCleanAir(projection)
			if err != nil {
				return nil, err
			}
			e := &wirelessCleanAirExecutor{client: c, desired: desired}
			parts = append(parts, configurationPart{name: key, executor: e})
			out.reports = append(out.reports, e.report)
		}
	}
	if len(parts) == 1 {
		out.configurationExecutor = parts[0].executor
	} else {
		out.configurationExecutor = &wirelessFeatureGroup{configurationGroup: &configurationGroup{parts: parts}}
	}
	return out, nil
}
