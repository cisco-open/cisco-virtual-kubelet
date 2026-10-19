// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"net/url"
	"strconv"
)

const wirelessFeatureSummaryPath = "/dna/intent/api/v1/featureTemplates/wireless/summary"

type wirelessFeatureInstance struct {
	ID     string `json:"id"`
	Name   string `json:"designName"`
	System *bool  `json:"systemTemplate"`
}

// Feature summary pagination counts instances inside the selected type group,
// not outer groups. This endpoint permits at most 25 instances per page.
func (c *client) wirelessFeatureInstances(ctx context.Context, kind string) (map[string]wirelessFeatureInstance, error) {
	out := map[string]wirelessFeatureInstance{}
	ids := map[string]bool{}
	expected := -1
	for page := 0; page < 400; page++ {
		raw, err := c.get(ctx, wirelessFeatureSummaryPath, url.Values{"type": {kind}, "offset": {strconv.Itoa(1 + page*25)}, "limit": {"25"}}, 200)
		if err != nil {
			return nil, err
		}
		groups, err := decodeList[struct {
			Type      string                    `json:"type"`
			Count     *int                      `json:"count"`
			Instances []wirelessFeatureInstance `json:"instances"`
		}](raw)
		if err != nil {
			return nil, err
		}
		if len(groups) > 1 {
			return nil, errInvalidResponse
		}
		if len(groups) == 0 {
			if expected >= 0 && len(out) != expected {
				return nil, errInvalidResponse
			}
			return out, nil
		}
		group := groups[0]
		if group.Type != kind || group.Count == nil || *group.Count < len(group.Instances) || group.Instances == nil || len(group.Instances) > 25 {
			return nil, errInvalidResponse
		}
		if expected < 0 {
			expected = *group.Count
		} else if expected != *group.Count {
			return nil, errInvalidResponse
		}
		for _, v := range group.Instances {
			if !validAPIID(v.ID) || !validProfileName(v.Name) || v.System == nil || ids[v.ID] {
				return nil, errInvalidResponse
			}
			if _, ok := out[v.Name]; ok {
				return nil, errInvalidResponse
			}
			ids[v.ID] = true
			out[v.Name] = v
		}
		if len(group.Instances) < 25 {
			if len(out) != expected {
				return nil, errInvalidResponse
			}
			return out, nil
		}
	}
	return nil, errInvalidResponse
}
