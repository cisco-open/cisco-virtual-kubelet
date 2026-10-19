// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Keep the names-only parser strict. This projection admits the pinned model's
// optional anchor_site while preventing native IDs or unknown fields from being
// silently discarded. Null has the same meaning as omission in NetAsCode.
func projectFabricL3Anchors(value any) ([]any, map[string]string, error) {
	rows, ok := value.([]any)
	if !ok || len(rows) == 0 || len(rows) > 1000 {
		return nil, nil, errInvalidResponse
	}
	out := make([]any, 0, len(rows))
	anchors := map[string]string{}
	for _, row := range rows {
		m, ok := row.(map[string]any)
		if !ok {
			return nil, nil, errInvalidResponse
		}
		for k := range m {
			if k != "name" && k != "anchor_site" {
				return nil, nil, errInvalidResponse
			}
		}
		name, ok := m["name"].(string)
		if !ok || !validProfileName(name) {
			return nil, nil, errInvalidResponse
		}
		if value, present := m["anchor_site"]; present && value != nil {
			path, ok := value.(string)
			if !ok || !validSitePath(path) || path == "Global" || name == "INFRA_VN" || name == "DEFAULT_VN" {
				return nil, nil, errInvalidResponse
			}
			anchors[name] = path
		}
		out = append(out, map[string]any{"name": name})
	}
	return out, anchors, nil
}

func (e *fabricL3Executor) resolveAnchors(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	if len(e.anchors) == 0 {
		return out, nil
	}
	sites, err := e.client.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	fabrics, err := e.client.fabricSites(ctx)
	if err != nil {
		return nil, err
	}
	byPath := map[string]string{}
	for _, site := range sites {
		if f, ok := fabrics[site.ID]; ok {
			byPath[site.NameHierarchy] = f.ID
		}
	}
	for name, path := range e.anchors {
		id := byPath[path]
		if id == "" {
			return nil, errConfigurationBusy
		}
		out[name] = id
	}
	return out, nil
}

// Anchoring changes physical forwarding. Gate the entire composition, not just
// the final VN write, until group admission and device handoff are enabled.
type fabricAnchorAdmissionGate struct{ configurationExecutor }

func (*fabricAnchorAdmissionGate) next(context.Context) (*configurationWrite, error) {
	return nil, errConfigurationDeviceAdmission
}
func (*fabricAnchorAdmissionGate) verify(context.Context, *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	return false, errConfigurationDeviceAdmission
}
