// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"sort"
)

type templateCompositeReference struct{ TemplateID, VersionID, MemberID string }

type templateCompositeView struct {
	summary   templateSummary
	draft     map[string]any
	committed map[string]any
}

// Scan structural member IDs, never UUID-looking text in content/parameters.
// Explicit regular definitions have no member collection; composite definitions
// must supply an actual collection. Missing/unknown shapes cannot prove absence.
func templateCompositeMembers(body map[string]any) ([]string, error) {
	composite, ok := body["composite"].(bool)
	if !ok {
		return nil, errInvalidResponse
	}
	raw, exists := body["containingTemplates"]
	if !composite && (!exists || raw == nil) {
		return []string{}, nil
	}
	members, ok := raw.([]any)
	if !ok {
		return nil, errInvalidResponse
	}
	if !composite && len(members) != 0 {
		return nil, errInvalidResponse
	}
	if len(members) > 10000 {
		return nil, errInvalidResponse
	}
	out := make([]string, 0, len(members))
	seen := map[string]bool{}
	for _, raw := range members {
		entry, ok := raw.(map[string]any)
		if !ok {
			return nil, errInvalidResponse
		}
		id, ok := entry["id"].(string)
		if !ok || !validAPIID(id) || seen[id] {
			return nil, errInvalidResponse
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// Include every committed version of every definition. Looking only at the
// current draft/latest commit would lose references retained by older versions.
// Target aliases include its base UUID and all native committed-version UUIDs.
// This is a consumer-class reader, not a Delete permit or complete removal graph.
func (c *client) templateCompositeReferences(ctx context.Context, target templateSummary) ([]templateCompositeReference, error) {
	if !validAPIID(target.ID) || !validAPIID(target.ProjectID) || target.Name == "" {
		return nil, errInvalidResponse
	}
	definitions, err := c.listTemplateDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	selected, exists := definitions[target.ProjectID+"#"+target.Name]
	if !exists || selected != target {
		return nil, errConfigurationOwned
	}
	summaries := make([]templateSummary, 0, len(definitions))
	baseIDs := map[string]bool{}
	for _, summary := range definitions {
		summaries = append(summaries, summary)
		baseIDs[summary.ID] = true
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].ID < summaries[j].ID })
	views := make([]templateCompositeView, 0, len(summaries))
	aliases := map[string]bool{target.ID: true}
	versionIDs := map[string]bool{}
	for _, summary := range summaries {
		draft, err := c.readTemplateDefinition(ctx, summary)
		if err != nil {
			return nil, err
		}
		footprint, err := c.templateVersionFootprint(ctx, summary)
		if err != nil {
			return nil, err
		}
		entries, err := templateVersionEntries(footprint, summary.ID)
		if err != nil {
			return nil, err
		}
		bodies, err := templateVersionBodies(footprint)
		if err != nil {
			return nil, err
		}
		if len(entries) != len(bodies) {
			return nil, errInvalidResponse
		}
		for id := range bodies {
			if _, exists := entries[id]; !exists || baseIDs[id] || versionIDs[id] {
				return nil, errInvalidResponse
			}
			versionIDs[id] = true
			if summary.ID == target.ID {
				aliases[id] = true
			}
		}
		views = append(views, templateCompositeView{summary: summary, draft: draft, committed: bodies})
	}
	out := []templateCompositeReference{}
	collect := func(summary templateSummary, version string, body map[string]any) error {
		members, err := templateCompositeMembers(body)
		if err != nil {
			return err
		}
		for _, id := range members {
			if aliases[id] {
				out = append(out, templateCompositeReference{TemplateID: summary.ID, VersionID: version, MemberID: id})
			}
		}
		return nil
	}
	for _, view := range views {
		if err := collect(view.summary, "", view.draft); err != nil {
			return nil, err
		}
		for id, raw := range view.committed {
			body, ok := raw.(map[string]any)
			if !ok {
				return nil, errInvalidResponse
			}
			if err := collect(view.summary, id, body); err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.TemplateID != b.TemplateID {
			return a.TemplateID < b.TemplateID
		}
		if a.VersionID != b.VersionID {
			return a.VersionID < b.VersionID
		}
		return a.MemberID < b.MemberID
	})
	return out, nil
}
func (c *client) stableTemplateCompositeReferences(ctx context.Context, target templateSummary) ([]templateCompositeReference, error) {
	first, err := c.templateCompositeReferences(ctx, target)
	if err != nil {
		return nil, err
	}
	second, err := c.templateCompositeReferences(ctx, target)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(first, second) {
		return nil, errConfigurationStale
	}
	return first, nil
}
