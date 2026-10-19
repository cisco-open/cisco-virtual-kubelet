// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
)

const templateReferenceProfilesRoot = "/dna/intent/api/v1/templates/"

type templateProfileReference struct{ ProfileID, Kind string }

func (c *client) templateAttachedProfileCount(ctx context.Context, path string) (int, error) {
	raw, err := c.get(ctx, path+"/count", nil, 200)
	if err != nil {
		return 0, err
	}
	var envelope struct {
		Response struct {
			Count *int `json:"count"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Response.Count == nil || *envelope.Response.Count < 0 || *envelope.Response.Count > 10000 {
		return 0, errInvalidResponse
	}
	return *envelope.Response.Count, nil
}

// This documented endpoint is unpaged. Do not invent offset/limit semantics or
// treat a short response as complete: require matching before/list/after counts.
// Unsupported, partial, malformed and changing views return no partial result.
func (c *client) templateAttachedProfiles(ctx context.Context, target string) ([]string, error) {
	if !validAPIID(target) {
		return nil, errInvalidResponse
	}
	path := templateReferenceProfilesRoot + target + "/networkProfilesForSites"
	before, err := c.templateAttachedProfileCount(ctx, path)
	if err != nil {
		return nil, err
	}
	raw, err := c.get(ctx, path, nil, 200)
	if err != nil {
		return nil, err
	}
	rows, err := decodeList[struct {
		ID string `json:"id"`
	}](raw)
	if err != nil {
		return nil, err
	}
	if len(rows) > 10000 {
		return nil, errInvalidResponse
	}
	ids := make([]string, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		if !validAPIID(row.ID) || seen[row.ID] {
			return nil, errInvalidResponse
		}
		seen[row.ID] = true
		ids = append(ids, row.ID)
	}
	after, err := c.templateAttachedProfileCount(ctx, path)
	if err != nil {
		return nil, err
	}
	if before != after || len(ids) != before {
		return nil, errConfigurationStale
	}
	sort.Strings(ids)
	return ids, nil
}

// Cross-check the independent legacy switching view, including onboarding
// references that a reverse Day-N association view alone cannot prove absent.
// Opaque site.settings is never interpreted, hashed or exposed as reference data.
func (c *client) templateProfileReferences(ctx context.Context, target string) ([]templateProfileReference, error) {
	modern, err := c.templateAttachedProfiles(ctx, target)
	if err != nil {
		return nil, err
	}
	out := []templateProfileReference{}
	for _, id := range modern {
		out = append(out, templateProfileReference{ProfileID: id, Kind: "attached"})
	}
	profiles, err := c.listSwitchingProfiles(ctx)
	if err != nil {
		return nil, err
	}
	for _, summary := range profiles {
		actual, err := c.readSwitchingProfile(ctx, summary)
		if err != nil {
			return nil, err
		}
		groups := map[string]bool{}
		for _, attribute := range actual.Attributes {
			if attribute.Key == "site.settings" {
				continue
			}
			if attribute.Key != "cli.templates" && attribute.Key != "day0.templates" || groups[attribute.Key] {
				return nil, errInvalidResponse
			}
			groups[attribute.Key] = true
			seen := map[string]bool{}
			for _, reference := range attribute.Attributes {
				if reference.Key != "template.id" || !validAPIID(reference.Value) || seen[reference.Value] {
					return nil, errInvalidResponse
				}
				seen[reference.Value] = true
				if reference.Value == target {
					out = append(out, templateProfileReference{ProfileID: actual.ID, Kind: attribute.Key})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProfileID != out[j].ProfileID {
			return out[i].ProfileID < out[j].ProfileID
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

// Repeat whole observations, not just a count. Equal counts with changed UUIDs
// are not a stable view. This reader grants no mutation or Delete capability;
// destructive callers still need source/owner/native-state and all other
// consumer-class guards immediately before submission.
func (c *client) stableTemplateProfileReferences(ctx context.Context, target string) ([]templateProfileReference, error) {
	first, err := c.templateProfileReferences(ctx, target)
	if err != nil {
		return nil, err
	}
	second, err := c.templateProfileReferences(ctx, target)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(first, second) {
		return nil, errConfigurationStale
	}
	return first, nil
}
