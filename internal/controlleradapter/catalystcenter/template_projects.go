// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"net/http"
	"net/url"
	"reflect"
	"strings"
)

const templateProjectsPath = "/dna/intent/api/v1/template-programmer/project"

type templateProject struct {
	ID          string  `json:"id,omitempty"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

func parseTemplateProjects(raw []byte) ([]templateProject, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errors.New("template project model requires only templates")
	}
	section, ok := root["templates"].(map[string]any)
	if !ok || len(section) != 1 {
		return nil, errors.New("unsupported template fields")
	}
	list, ok := section["projects"].([]any)
	if !ok || len(list) == 0 || len(list) > 1000 {
		return nil, errors.New("invalid template project list")
	}
	out := []templateProject{}
	seen := map[string]bool{}
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errors.New("invalid project")
		}
		for key := range m {
			if key != "name" && key != "description" {
				return nil, errors.New("template content and tags are not implemented")
			}
		}
		name, ok := m["name"].(string)
		if !ok || strings.TrimSpace(name) != name || name == "" || len(name) > 256 || strings.ContainsAny(name, "#\r\n\x00") {
			return nil, errors.New("invalid project name")
		}
		if seen[name] {
			return nil, errors.New("duplicate template project")
		}
		seen[name] = true
		p := templateProject{Name: name}
		if v, present := m["description"]; present && v != nil {
			d, ok := v.(string)
			if !ok || len(d) > 4096 {
				return nil, errors.New("invalid project description")
			}
			p.Description = &d
		}
		out = append(out, p)
	}
	return out, nil
}
func (c *client) listTemplateProjects(ctx context.Context) (map[string]templateProject, error) {
	raw, err := c.get(ctx, templateProjectsPath, nil, 200)
	if err != nil {
		return nil, err
	}
	var list []templateProject
	if json.Unmarshal(raw, &list) != nil || list == nil || len(list) > 10000 {
		return nil, errInvalidResponse
	}
	out := map[string]templateProject{}
	ids := map[string]bool{}
	for _, p := range list {
		if !validAPIID(p.ID) || p.Name == "" || ids[p.ID] {
			return nil, errInvalidResponse
		}
		if _, ok := out[p.Name]; ok {
			return nil, errInvalidResponse
		}
		out[p.Name] = p
		ids[p.ID] = true
	}
	return out, nil
}
func templateProjectMatches(w, g templateProject) bool {
	return w.Description == nil || (g.Description != nil && *w.Description == *g.Description)
}
func (c *client) reportTemplateProjects(ctx context.Context, want []templateProject) ([]configapi.NetworkControllerDriftEntry, error) {
	observed, err := c.listTemplateProjects(ctx)
	if err != nil {
		return nil, err
	}
	drift := []configapi.NetworkControllerDriftEntry{}
	for _, w := range want {
		g, exists := observed[w.Name]
		if w.Name == "Onboarding Configuration" {
			if !exists {
				return nil, errors.New("built-in onboarding project is missing")
			}
			continue
		}
		change := configapi.NetworkControllerDriftChangeUpdate
		if !exists {
			change = configapi.NetworkControllerDriftChangeAdd
		} else if templateProjectMatches(w, g) {
			continue
		}
		drift = append(drift, configapi.NetworkControllerDriftEntry{Section: "templates", Resource: w.Name, Path: "/projects", Change: change})
	}
	return drift, nil
}

type templateProjectExecutor struct {
	submissionGuard func(context.Context, string, string, configurationResourceIdentity) error
	client          *client
	desired         []templateProject
}

func (e *templateProjectExecutor) count() int { return len(e.desired) }
func (e *templateProjectExecutor) next(ctx context.Context) (*configurationWrite, error) {
	if len(e.desired) == 0 {
		return nil, nil
	}
	observed, err := e.client.listTemplateProjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, w := range e.desired {
		if w.Name == "Onboarding Configuration" {
			if _, ok := observed[w.Name]; !ok {
				return nil, errors.New("built-in onboarding project is missing")
			}
		}
	}
	for _, w := range e.desired {
		if w.Name == "Onboarding Configuration" {
			continue
		}
		got, exists := observed[w.Name]
		if exists && templateProjectMatches(w, got) {
			continue
		}
		method, action, path := http.MethodPost, "Create", templateProjectsPath
		query := url.Values{}
		payload := w
		if exists {
			method, action = http.MethodPut, "Update"
			payload.ID = got.ID
			query.Set("id", got.ID)
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		intent, err := json.Marshal(e.desired)
		if err != nil {
			return nil, errInvalidResponse
		}
		selected := func(projects map[string]templateProject) map[string]templateProject {
			result := map[string]templateProject{}
			for _, desired := range e.desired {
				if project, exists := projects[desired.Name]; exists {
					result[desired.Name] = project
				}
			}
			return result
		}
		before := selected(observed)
		write := &configurationWrite{resource: w.Name, action: action, identity: configurationResourceIdentity{target: got.ID}, submit: func(ctx context.Context) (Task, error) {
			raw, err := e.client.mutateQuery(ctx, method, path, query, body)
			if err != nil {
				return Task{}, err
			}
			var receipt struct {
				Response Task `json:"response"`
			}
			if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
				return Task{}, errInvalidResponse
			}
			return Task{ID: receipt.Response.ID}, nil
		}}
		guardConfigurationPreflight(write, func(ctx context.Context) error {
			current, err := json.Marshal(e.desired)
			if err != nil || !bytes.Equal(intent, current) {
				return errConfigurationStale
			}
			fresh, err := e.client.listTemplateProjects(ctx)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(before, selected(fresh)) {
				return errConfigurationStale
			}
			if e.submissionGuard != nil {
				return e.submissionGuard(ctx, write.resource, write.action, write.identity)
			}
			return nil
		})
		return write, nil
	}
	return nil, nil
}
func (e *templateProjectExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if (op.Action != "Create" && op.Action != "Update") || op.ParentID != "" || (op.Action == "Update" && !validAPIID(op.TargetID)) {
		return false, errConfigurationTransition
	}
	observed, err := e.client.listTemplateProjects(ctx)
	if err != nil {
		return false, err
	}
	for _, w := range e.desired {
		if w.Name == op.Resource && w.Name != "Onboarding Configuration" {
			g, ok := observed[w.Name]
			if !ok {
				return false, nil
			}
			if op.TargetID != "" && op.TargetID != g.ID {
				return false, errors.New("template project identity changed")
			}
			return templateProjectMatches(w, g), nil
		}
	}
	return false, errConfigurationTransition
}
