// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
)

type wirelessSSIDMetadata struct {
	id, name, inheritedSiteID string
	sensor                    bool
	mainCredential            bool
	unmapped                  bool
	fields                    map[string]any
}

func wirelessSSIDPath(siteID string) string {
	return sitesPath + "/" + url.PathEscape(siteID) + "/wirelessSettings/ssids"
}

// Project public attributes only. API responses may contain main and multi-PSK
// passphrases; no returned secret is retained, logged or reused for a write.
func (c *client) wirelessSSIDInventory(ctx context.Context, siteID string) (map[string]wirelessSSIDMetadata, error) {
	if !validAPIID(siteID) {
		return nil, errInvalidResponse
	}
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	rows, err := listInventoryQueryWithGet(ctx, get, wirelessSSIDPath(siteID), nil, func(raw json.RawMessage) string {
		var row struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &row)
		return row.ID
	})
	if err != nil {
		return nil, err
	}
	out := map[string]wirelessSSIDMetadata{}
	for _, raw := range rows {
		var row map[string]any
		if json.Unmarshal(raw, &row) != nil {
			return nil, errInvalidResponse
		}
		p := wirelessSSIDMetadata{fields: map[string]any{}}
		known := map[string]bool{"id": true, "ssid": true, "passphrase": true, "multiPSKSettings": true, "inheritedSiteId": true, "inheritedSiteUUID": true, "ipv6AclName": true, "urlAclName": true, "randomMacFilterEnabled": true, "inheritedSiteName": true, "inheritedSiteNameHierarchy": true, "isSensorPnp": true, "isCustomNasIdOptions": true}
		for _, field := range wirelessSSIDFields {
			known[field.path] = true
		}
		for _, path := range wirelessSSIDSets {
			known[path] = true
		}
		// The lab release returns disabled load-balancing placeholders absent
		// from the pinned write model. Never overwrite an enabled extension.
		for _, key := range []string{"isLoadBalancingEnabledForAuthGroup", "isLoadBalancingEnabledForAcctGroup"} {
			known[key] = true
			if v := row[key]; v != nil {
				flag, ok := v.(bool)
				if !ok {
					return nil, errInvalidResponse
				}
				if flag {
					p.unmapped = true
				}
			}
		}
		// Some appliance releases emit additional empty ACL placeholders and a
		// duplicate MAC-filter flag. Nonempty unsupported ACLs still fence PUT;
		// accepting them without a write contract could silently remove policy.
		for _, key := range []string{"ipv6AclName", "urlAclName"} {
			if v := row[key]; v != nil {
				value, ok := v.(string)
				if !ok {
					return nil, errInvalidResponse
				}
				if value != "" {
					p.unmapped = true
				}
			}
		}
		if v := row["randomMacFilterEnabled"]; v != nil {
			alias, ok := v.(bool)
			canonical, present := row["isRandomMacFilterEnabled"].(bool)
			if !ok || !present || alias != canonical {
				return nil, errInvalidResponse
			}
		}
		for key := range row {
			if !known[key] {
				p.unmapped = true
			}
		}
		if value, exists := row["passphrase"]; exists && value != nil {
			secret, ok := value.(string)
			if !ok {
				return nil, errInvalidResponse
			}
			p.mainCredential = secret != ""
		}
		if value, exists := row["isCustomNasIdOptions"]; exists && value != nil {
			flag, ok := value.(bool)
			if !ok {
				return nil, errInvalidResponse
			}
			p.fields["isCustomNasIdOptions"] = flag
		}
		p.id, _ = row["id"].(string)
		p.name, _ = row["ssid"].(string)
		if !validAPIID(p.id) || p.name == "" || len(p.name) > 32 || strings.ContainsAny(p.name, "\x00\r\n") {
			return nil, errInvalidResponse
		}
		if _, exists := out[p.name]; exists {
			return nil, errInvalidResponse
		}
		for _, field := range wirelessSSIDFields {
			v, exists := row[field.path]
			if !exists || v == nil {
				continue
			}
			// Read responses use empty strings and zero values for disabled or
			// inapplicable settings. Preserve these observations without adding
			// them to the accepted manifest enum/range constraints.
			switch field.kind {
			case "bool":
				if _, ok := v.(bool); !ok {
					return nil, errInvalidResponse
				}
			case "int":
				n, err := wirelessAPInteger(v)
				if err != nil {
					return nil, errInvalidResponse
				}
				v = n
			case "string":
				s, ok := v.(string)
				if !ok || strings.ContainsAny(s, "\x00\r\n") {
					return nil, errInvalidResponse
				}
			}
			p.fields[field.path] = v
		}
		for _, required := range []string{"authType", "wlanType"} {
			if v, ok := p.fields[required].(string); !ok || v == "" {
				return nil, errInvalidResponse
			}
		}
		for _, path := range wirelessSSIDSets {
			if v, exists := row[path]; exists && v != nil {
				values, err := wirelessSSIDStringSet(v)
				if err != nil {
					return nil, errInvalidResponse
				}
				p.fields[path] = values
			}
		}
		if v, exists := row["multiPSKSettings"]; exists && v != nil {
			entries, ok := v.([]any)
			if !ok || len(entries) > 5 {
				return nil, errInvalidResponse
			}
			metadata := []map[string]any{}
			seen := map[int64]bool{}
			for _, entry := range entries {
				m, ok := entry.(map[string]any)
				if !ok {
					return nil, errInvalidResponse
				}
				priority, err := wirelessAPInteger(m["priority"])
				if err != nil || priority < 0 || priority > 4 || seen[priority] {
					return nil, errInvalidResponse
				}
				seen[priority] = true
				public := map[string]any{"priority": priority}
				for key := range m {
					if key != "priority" && key != "passphraseType" && key != "passphrase" {
						p.unmapped = true
					}
				}
				if v, exists := m["passphraseType"]; exists {
					if v != "ASCII" && v != "HEX" {
						return nil, errInvalidResponse
					}
					public["passphraseType"] = v
				}
				metadata = append(metadata, public)
			}
			// Priority is the model identity; response order is not a change to
			// the selected object and must not invalidate submission preflight.
			slices.SortFunc(metadata, func(a, b map[string]any) int {
				return int(a["priority"].(int64) - b["priority"].(int64))
			})
			p.fields["multiPSKSettings"] = metadata
		}
		for _, key := range []string{"inheritedSiteId", "inheritedSiteUUID"} {
			if v, exists := row[key]; exists && v != nil {
				id, ok := v.(string)
				if !ok || (id != "" && !validAPIID(id)) {
					return nil, errInvalidResponse
				}
				if id != "" {
					if p.inheritedSiteID != "" && p.inheritedSiteID != id {
						return nil, errInvalidResponse
					}
					p.inheritedSiteID = id
				}
			}
		}
		if v, exists := row["isSensorPnp"]; exists && v != nil {
			flag, ok := v.(bool)
			if !ok {
				return nil, errInvalidResponse
			}
			p.sensor = flag
		}
		out[p.name] = p
	}
	return out, nil
}
