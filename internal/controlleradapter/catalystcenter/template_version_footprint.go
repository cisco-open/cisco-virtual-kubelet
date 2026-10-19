// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
)

// Preserve unknown metadata and every committed body, not just the latest ID.
// Values leave this function only as input to the private keyed comparison.
func (c *client) templateVersionFootprint(ctx context.Context, base templateSummary) (any, error) {
	if !validAPIID(base.ID) || !validAPIID(base.ProjectID) || base.Name == "" {
		return nil, errInvalidResponse
	}
	raw, err := c.get(ctx, templateVersionsPath+"/"+base.ID, nil, 200)
	if err != nil {
		return nil, err
	}
	if _, err = decodeLatestTemplateVersion(raw, base.ID); err != nil {
		return nil, err
	}
	metadata, err := decodeTemplateNativeJSON(raw)
	if err != nil {
		return nil, err
	}
	var groups []struct {
		Versions []struct {
			ID      string          `json:"id"`
			Version json.RawMessage `json:"version"`
		} `json:"versionsInfo"`
	}
	if json.Unmarshal(raw, &groups) != nil {
		return nil, errInvalidResponse
	}
	bodies := map[string]any{}
	for _, g := range groups {
		for _, v := range g.Versions {
			if len(v.Version) == 0 && v.ID == base.ID {
				continue
			}
			n, err := templateVersionNumber(v.Version)
			if err != nil {
				return nil, err
			}
			if n == 0 {
				continue
			}
			encoded, err := c.get(ctx, templatesPath+"/"+v.ID, nil, 200)
			if err != nil {
				return nil, err
			}
			_, err = decodeCommittedTemplate(encoded, base, templateVersion{ID: v.ID, Number: n})
			if err != nil {
				return nil, err
			}
			body, err := decodeTemplateNativeJSON(encoded)
			if err != nil {
				return nil, err
			}
			bodies[v.ID] = body
		}
	}
	return map[string]any{"metadata": metadata, "committed": bodies}, nil
}
