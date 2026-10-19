// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"net/url"
	"reflect"
)

const templatesPath = "/dna/intent/api/v1/template-programmer/template"

type templateSummary struct {
	ID        string `json:"templateId"`
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
}

func (c *client) listTemplateDefinitions(ctx context.Context) (map[string]templateSummary, error) {
	raw, err := c.get(ctx, templatesPath, url.Values{"unCommitted": {"true"}}, 200)
	if err != nil {
		return nil, err
	}
	var list []templateSummary
	if json.Unmarshal(raw, &list) != nil || list == nil || len(list) > 10000 {
		return nil, errInvalidResponse
	}
	result := map[string]templateSummary{}
	ids := map[string]bool{}
	for _, s := range list {
		key := s.ProjectID + "#" + s.Name
		if !validAPIID(s.ID) || !validAPIID(s.ProjectID) || s.Name == "" || ids[s.ID] {
			return nil, errInvalidResponse
		}
		if _, ok := result[key]; ok {
			return nil, errInvalidResponse
		}
		result[key] = s
		ids[s.ID] = true
	}
	return result, nil
}
func (c *client) readTemplateDefinition(ctx context.Context, s templateSummary) (map[string]any, error) {
	raw, err := c.get(ctx, templatesPath+"/"+s.ID, url.Values{"unCommitted": {"true"}}, 200)
	if err != nil {
		return nil, err
	}
	decoded, err := decodeTemplateNativeJSON(raw)
	if err != nil {
		return nil, err
	}
	body, ok := decoded.(map[string]any)
	if !ok || body["id"] != s.ID || body["name"] != s.Name || body["projectId"] != s.ProjectID {
		return nil, errInvalidResponse
	}
	return body, nil
}

// Compare managed fields only. The API adds parameter metadata; never copy it
// into desired payloads or publish template/variable values as drift evidence.
func templateValueMatches(want, got any) bool {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range w {
			if !templateValueMatches(value, g[key]) {
				return false
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(w) != len(g) {
			return false
		}
		for i := range w {
			if !templateValueMatches(w[i], g[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(want, got)
	}
}
func templateDefinitionMatches(want, got map[string]any) bool {
	for key, value := range want {
		// Native replies may omit or null an explicitly cleared constraint.
		// This equivalence is field-specific; nonempty constraints stay exact and
		// private baseline capture still fingerprints every native field/value.
		if key == "softwareVersion" && value == "" && (got[key] == nil || got[key] == "") {
			continue
		}
		if key == "deviceTypes" || key == "templateParams" {
			w, ok := value.([]any)
			if !ok {
				return false
			}
			g, ok := got[key].([]any)
			if !ok || len(w) != len(g) {
				return false
			}
			used := make([]bool, len(g))
			for _, item := range w {
				found := false
				for j, actual := range g {
					matches := templateValueMatches(item, actual)
					if key == "templateParams" {
						matches = templateParameterMatches(item, actual)
					}
					if !used[j] && matches {
						used[j] = true
						found = true
						break
					}
				}
				if !found {
					return false
				}
			}
		} else if !templateValueMatches(value, got[key]) {
			return false
		}
	}
	return true
}

// Choice labels and values are a complete managed map, unlike native parameter
// records, which contain additional server-owned metadata. Subset comparison
// would hide removed choices and make an explicit empty map a permanent no-op.
func templateParameterMatches(want, got any) bool {
	w, ok := want.(map[string]any)
	if !ok {
		return false
	}
	g, ok := got.(map[string]any)
	if !ok || !templateValueMatches(w, g) {
		return false
	}
	selection, ok := w["selection"].(map[string]any)
	if !ok {
		return true
	}
	choices, managed := selection["selectionValues"]
	if !managed {
		return true
	}
	actual, ok := g["selection"].(map[string]any)
	return ok && reflect.DeepEqual(choices, actual["selectionValues"])
}
func (c *client) reportTemplateIntent(ctx context.Context, intent templateIntent) ([]configapi.NetworkControllerDriftEntry, error) {
	drift := []configapi.NetworkControllerDriftEntry{}
	if len(intent.projects) > 0 {
		d, err := c.reportTemplateProjects(ctx, intent.projects)
		if err != nil {
			return nil, err
		}
		drift = append(drift, d...)
	}
	if len(intent.tags) > 0 {
		d, err := c.reportTags(ctx, intent.tags)
		if err != nil {
			return nil, err
		}
		drift = append(drift, d...)
	}
	memberships := &templateMembershipExecutor{client: c, intent: intent}
	if memberships.count() > 0 {
		d, err := memberships.report(ctx)
		if err != nil {
			return nil, err
		}
		drift = append(drift, d...)
	}
	if len(intent.definitions) == 0 {
		return drift, nil
	}
	projects, err := c.listTemplateProjects(ctx)
	if err != nil {
		return nil, err
	}
	actual, err := c.listTemplateDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	for _, w := range intent.definitions {
		project, exists := projects[w.identity.Project]
		summary, present := actual[project.ID+"#"+w.identity.Name]
		change := configapi.NetworkControllerDriftChangeAdd
		if exists && present {
			got, err := c.readTemplateDefinition(ctx, summary)
			if err != nil {
				return nil, err
			}
			payload := map[string]any{}
			for k, v := range w.payload {
				payload[k] = v
			}
			if w.identity.Composite {
				members := []any{}
				for _, key := range w.identity.Members {
					var member desiredTemplate
					found := false
					for _, candidate := range intent.definitions {
						if candidate.identity.Key() == key {
							member = candidate
							found = true
							break
						}
					}
					m, ok := actual[projects[member.identity.Project].ID+"#"+member.identity.Name]
					if !found || !ok {
						members = append(members, map[string]any{"id": ""})
						continue
					}
					members = append(members, map[string]any{"id": m.ID, "name": member.identity.Name, "language": member.payload["language"], "projectName": member.identity.Project})
				}
				payload["containingTemplates"] = members
			}
			if templateDefinitionMatches(payload, got) {
				continue
			}
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		drift = append(drift, configapi.NetworkControllerDriftEntry{Section: "templates", Resource: w.identity.Key(), Path: "/definition", Change: change})
	}
	return drift, nil
}
