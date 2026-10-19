// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"slices"
	"sort"
	"strings"
)

type fabricDeviceIntent struct {
	inventory                     inventoryDeviceIntent
	state, fabricSite, fabricZone string
	roles                         []string
	border                        *fabricBorderSettings
}

// Compile the inventory/fabric-device projection without accepting other
// resources on their behalf. The runtime composition must retain provisioning,
// assignment and role prerequisites; ASSIGN below is only shared model validation.
func parseFabricDeviceIntents(raw []byte) ([]fabricDeviceIntent, error) {
	return parseFabricDeviceIntentsWithExistingRoles(raw, false)
}

// Existing-only projections may omit base roles. They cannot be passed to the
// fabric creation planner; the caller must bind an existing native membership.
func parseFabricDeviceIntentsWithExistingRoles(raw []byte, existingRoles bool) ([]fabricDeviceIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) < 1 || len(root) > 2 {
		return nil, errInvalidResponse
	}
	for k := range root {
		if k != "inventory" && k != "fabric" {
			return nil, errInvalidResponse
		}
	}
	inventory, ok := root["inventory"].(map[string]any)
	if !ok || len(inventory) != 1 {
		return nil, errInvalidResponse
	}
	rows, ok := inventory["devices"].([]any)
	if !ok || len(rows) == 0 || len(rows) > 1000 {
		return nil, errInvalidResponse
	}
	borders := map[string]*fabricBorderSettings{}
	if value, present := root["fabric"]; present {
		fabric, ok := value.(map[string]any)
		if !ok || len(fabric) != 1 {
			return nil, errInvalidResponse
		}
		list, ok := fabric["border_devices"].([]any)
		if !ok || len(list) > 1000 {
			return nil, errInvalidResponse
		}
		for _, row := range list {
			m, ok := row.(map[string]any)
			if !ok {
				return nil, errInvalidResponse
			}
			name, ok := m["name"].(string)
			if !ok || name == "" || borders[name] != nil {
				return nil, errInvalidResponse
			}
			fields := map[string]any{}
			for k, v := range m {
				if k != "name" {
					fields[k] = v
				}
			}
			settings, err := parseFabricBorderSettings(fields)
			if err != nil {
				return nil, err
			}
			borders[name] = settings
		}
	}
	out := []fabricDeviceIntent{}
	projections := []any{}
	for _, row := range rows {
		model, ok := row.(map[string]any)
		if !ok {
			return nil, errInvalidResponse
		}
		intent := fabricDeviceIntent{}
		intent.state, ok = model["state"].(string)
		if !ok || (intent.state != "PROVISION" && intent.state != "REPROVISION") {
			return nil, errInvalidResponse
		}
		intent.fabricSite, ok = model["fabric_site"].(string)
		if !ok || !validSitePath(intent.fabricSite) || intent.fabricSite == "Global" {
			return nil, errInvalidResponse
		}
		if value, present := model["fabric_zone"]; present && value != nil {
			intent.fabricZone, ok = value.(string)
			if !ok || !validSitePath(intent.fabricZone) || !strings.HasPrefix(intent.fabricZone, intent.fabricSite+"/") {
				return nil, errInvalidResponse
			}
		}
		roles, ok := model["fabric_roles"].([]any)
		if !ok || (len(roles) == 0 && !existingRoles) || len(roles) > 4 {
			return nil, errInvalidResponse
		}
		for _, v := range roles {
			role, ok := v.(string)
			if !ok || slices.Contains(intent.roles, role) {
				return nil, errInvalidResponse
			}
			switch role {
			case "CONTROL_PLANE_NODE", "EDGE_NODE", "BORDER_NODE":
			default:
				return nil, errInvalidResponse
			}
			intent.roles = append(intent.roles, role)
		}
		sort.Strings(intent.roles)
		name, _ := model["name"].(string)
		intent.border = borders[name]
		if slices.Contains(intent.roles, "BORDER_NODE") != (intent.border != nil) {
			return nil, errInvalidResponse
		}
		delete(borders, name)
		projection := map[string]any{}
		for k, v := range model {
			switch k {
			case "fabric_site", "fabric_zone", "fabric_roles":
				continue
			}
			projection[k] = v
		}
		projection["state"] = "ASSIGN"
		projections = append(projections, projection)
		out = append(out, intent)
	}
	if len(borders) > 0 {
		return nil, errInvalidResponse
	}
	encoded, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": projections}}})
	if err != nil {
		return nil, err
	}
	common, err := parseInventoryIntent(encoded)
	if err != nil {
		return nil, err
	}
	for i, d := range common.devices {
		out[i].inventory = d
		owner := out[i].fabricSite
		if out[i].fabricZone != "" {
			owner = out[i].fabricZone
		}
		if d.site != owner && !strings.HasPrefix(d.site, owner+"/") {
			return nil, errInvalidResponse
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].inventory.reference.Name < out[j].inventory.reference.Name })
	return out, nil
}
