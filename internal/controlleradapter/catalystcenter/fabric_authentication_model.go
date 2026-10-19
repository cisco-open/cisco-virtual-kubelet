// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

const fabricAuthResourceFamily = "fabric-auth:"

type fabricAuthIntent struct {
	site    fabricSiteIntent
	profile authProfile
}

// Extract only pinned site-template overrides. The existing fabric parser still
// validates the complete remaining model; no sibling/zone intent is discarded.
// Inputs are resolved NaC values, so the shared parser requires explicit fields
// rather than inventing Terraform defaults or borrowing mutable global values.
func projectFabricAuthentication(value any) ([]any, []fabricAuthIntent, error) {
	rows, ok := value.([]any)
	if !ok || len(rows) == 0 || len(rows) > 1000 {
		return nil, nil, errInvalidResponse
	}
	base := make([]any, 0, len(rows))
	intents := []fabricAuthIntent{}
	seen := map[string]bool{}
	for _, row := range rows {
		m, ok := row.(map[string]any)
		if !ok {
			return nil, nil, errInvalidResponse
		}
		copy := map[string]any{}
		for k, v := range m {
			copy[k] = v
		}
		base = append(base, copy)
		auth, ok := m["authentication_template"].(map[string]any)
		if !ok || len(auth) == 0 {
			return nil, nil, errInvalidResponse
		}
		if len(auth) == 1 {
			continue // Existing name-only site creation remains unchanged.
		}
		name, _ := auth["name"].(string)
		path, _ := m["name"].(string)
		pubsub, ok := m["pub_sub_enabled"].(bool)
		if !ok || !validSitePath(path) || path == "Global" || seen[path] {
			return nil, nil, errInvalidResponse
		}
		seen[path] = true
		global := map[string]any{}
		for k, v := range auth {
			if k != "bpdu_guard" {
				global[k] = v
			}
		}
		raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"authentication_templates": []any{global}}})
		if err != nil {
			return nil, nil, err
		}
		profiles, err := parseAuthTemplates(raw)
		if err != nil || len(profiles) != 1 {
			return nil, nil, errInvalidResponse
		}
		if value, present := auth["bpdu_guard"]; present {
			bpdu, ok := value.(bool)
			if !ok || name != "Closed Authentication" {
				return nil, nil, errInvalidResponse
			}
			profiles[0].BPDU = &bpdu
		}
		copy["authentication_template"] = map[string]any{"name": name}
		intents = append(intents, fabricAuthIntent{site: fabricSiteIntent{path: path, authentication: name, pubsub: pubsub}, profile: profiles[0]})
	}
	sort.Slice(intents, func(i, j int) bool { return intents[i].site.path < intents[j].site.path })
	return base, intents, nil
}

// Bind the hierarchy UUID in the immutable resource alongside the existing
// target/profile UUID and parent/fabric UUID journal slots.
func (w fabricAuthIntent) resource(siteID string) string {
	return fmt.Sprintf("%s%x", w.resourcePrefix(), sha256.Sum256([]byte(siteID)))
}

func (w fabricAuthIntent) resourcePrefix() string {
	raw, _ := json.Marshal([]string{w.site.path, w.profile.Name})
	return fmt.Sprintf("%sv1:%x:", fabricAuthResourceFamily, sha256.Sum256(raw))
}
