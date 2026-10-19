// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

const networkProfilesPath = "/api/v1/siteprofile"

type switchingProfileIntent struct {
	name                    string
	dayn, onboarding, sites []string
}

// Network profiles use base template UUIDs, including composites, rather than
// committed-version UUIDs. Profile membership does not deploy a template.
func parseNetworkProfiles(raw []byte) ([]switchingProfileIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errors.New("network profiles require a section-specific model")
	}
	section, ok := root["network_profiles"].(map[string]any)
	if !ok || len(section) != 1 {
		return nil, errors.New("unsupported network profile collection")
	}
	list, ok := section["switching"].([]any)
	if !ok || len(list) > 1000 {
		return nil, errors.New("invalid switching profile collection")
	}
	out := []switchingProfileIntent{}
	seen := map[string]bool{}
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errors.New("invalid switching profile")
		}
		name, ok := m["name"].(string)
		if !ok || !validProfileName(name) || seen[name] {
			return nil, errors.New("invalid or duplicate switching profile name")
		}
		seen[name] = true
		p := switchingProfileIntent{name: name}
		for key, value := range m {
			if key == "name" {
				continue
			}
			if key != "dayn_templates" && key != "onboarding_templates" && key != "sites" {
				return nil, errors.New("unsupported switching profile field")
			}
			values, ok := value.([]any)
			if !ok || len(values) > 1000 {
				return nil, errors.New("invalid profile references")
			}
			refs := []string{}
			unique := map[string]bool{}
			for _, value := range values {
				ref, ok := value.(string)
				if !ok || unique[ref] {
					return nil, errors.New("invalid or duplicate profile reference")
				}
				if key == "sites" {
					if !validSitePath(ref) || ref == "Global" {
						return nil, errors.New("invalid profile site")
					}
				} else {
					parts := strings.Split(ref, "#")
					if len(parts) > 2 {
						return nil, errors.New("invalid profile template reference")
					}
					for _, part := range parts {
						if !validProfileName(part) {
							return nil, errors.New("invalid profile template reference")
						}
					}
				}
				unique[ref] = true
				refs = append(refs, ref)
			}
			switch key {
			case "dayn_templates":
				p.dayn = refs
			case "onboarding_templates":
				p.onboarding = refs
			case "sites":
				p.sites = refs
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func validProfileName(name string) bool {
	return name != "" && len(name) <= 256 && strings.TrimSpace(name) == name && !strings.ContainsAny(name, "#\x00\r\n")
}

type networkProfile struct {
	ID         string                    `json:"siteProfileUuid,omitempty"`
	Name       string                    `json:"name"`
	Namespace  string                    `json:"namespace"`
	Attributes []networkProfileAttribute `json:"profileAttributes"`
}
type networkProfileAttribute struct {
	Key        string                   `json:"key"`
	Attributes []networkProfileTemplate `json:"attribs,omitempty"`
	// The server materializes inherited settings after site assignment. Keep
	// this opaque subtree in memory for updates; it can contain secrets and
	// must never enter status, unkeyed hashes, errors, logs or evidence.
	raw json.RawMessage
}

func (a *networkProfileAttribute) UnmarshalJSON(raw []byte) error {
	type plain networkProfileAttribute
	var value plain
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*a = networkProfileAttribute(value)
	if a.Key == "site.settings" {
		a.raw = append(json.RawMessage(nil), raw...)
	}
	return nil
}
func (a networkProfileAttribute) MarshalJSON() ([]byte, error) {
	if a.Key == "site.settings" && a.raw != nil {
		return a.raw, nil
	}
	type plain networkProfileAttribute
	return json.Marshal(plain(a))
}

type networkProfileTemplate struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// This legacy endpoint returns an unpaged summary collection. Details are
// fetched separately: an absent attribute list in the summary is not empty
// desired membership. Keep every namespace for reference checks; only the
// switching wrapper below may select definitions for the switching executor.
func (c *client) listNetworkProfileSummaries(ctx context.Context) ([]networkProfile, error) {
	raw, err := c.get(ctx, networkProfilesPath, nil, 200)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Response []networkProfile `json:"response"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Response == nil || len(envelope.Response) > 10000 {
		return nil, errInvalidResponse
	}
	ids := map[string]bool{}
	for _, p := range envelope.Response {
		if !validAPIID(p.ID) || p.Name == "" || p.Namespace == "" || ids[p.ID] {
			return nil, errInvalidResponse
		}
		ids[p.ID] = true
	}
	return envelope.Response, nil
}

func (c *client) listSwitchingProfiles(ctx context.Context) (map[string]networkProfile, error) {
	rows, err := c.listNetworkProfileSummaries(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]networkProfile{}
	for _, p := range rows {
		if p.Namespace != "switching" {
			continue
		}
		if _, duplicate := out[p.Name]; duplicate {
			return nil, errInvalidResponse
		}
		out[p.Name] = p
	}
	return out, nil
}

func (c *client) readSwitchingProfile(ctx context.Context, summary networkProfile) (networkProfile, error) {
	observed, err := c.readSwitchingProfileNative(ctx, summary)
	if err != nil {
		return networkProfile{}, err
	}
	return observed.profile, nil
}

func resolveProfileTemplate(ref string, projects map[string]templateProject, definitions map[string]templateSummary) (templateSummary, error) {
	if parts := strings.Split(ref, "#"); len(parts) == 2 {
		project, exists := projects[parts[0]]
		if exists {
			if d, exists := definitions[project.ID+"#"+parts[1]]; exists {
				return d, nil
			}
		}
		return templateSummary{}, errors.New("profile template dependency is missing")
	}
	var found templateSummary
	for _, d := range definitions {
		if d.Name != ref {
			continue
		}
		if found.ID != "" {
			return templateSummary{}, errors.New("profile template reference is ambiguous; qualify its project")
		}
		found = d
	}
	if found.ID == "" {
		return found, errors.New("profile template dependency is missing")
	}
	return found, nil
}

func resolvedSwitchingProfile(w switchingProfileIntent, projects map[string]templateProject, definitions map[string]templateSummary) (networkProfile, error) {
	p := networkProfile{Name: w.name, Namespace: "switching", Attributes: []networkProfileAttribute{}}
	for _, group := range []struct {
		key  string
		refs []string
	}{{"cli.templates", w.dayn}, {"day0.templates", w.onboarding}} {
		a := networkProfileAttribute{Key: group.key}
		seen := map[string]bool{}
		for _, ref := range group.refs {
			d, err := resolveProfileTemplate(ref, projects, definitions)
			if err != nil {
				return networkProfile{}, err
			}
			if seen[d.ID] {
				return networkProfile{}, errors.New("profile aliases select the same template")
			}
			seen[d.ID] = true
			a.Attributes = append(a.Attributes, networkProfileTemplate{Key: "template.id", Value: d.ID})
		}
		p.Attributes = append(p.Attributes, a)
	}
	return p, nil
}
