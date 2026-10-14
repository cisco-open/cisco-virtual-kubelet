// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"errors"
	"net/url"
	"regexp"
)

var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Observation is kept in process memory, never in Kubernetes status.
type Observation struct {
	ID       string
	Body     map[string]any
	Attached bool
}

func (c *Client) Observe(ctx context.Context, r Resource) (*Observation, error) {
	if r.Kind == "tag" {
		return c.observeTag(ctx, r)
	}
	if r.Kind == "cloud-setting" {
		return c.observeCloudSettings(ctx)
	}
	if r.Kind == "localized-policy" || r.Kind == "centralized-policy" || r.Kind == "security-policy" {
		return c.observeLocalizedPolicy(ctx, r)
	}
	if r.Kind == "classic-definition" {
		return c.observeClassicDefinition(ctx, r)
	}
	if r.Kind == "custom-app" {
		return c.observeCustomApplication(ctx, r)
	}
	if r.Kind == "hierarchy" {
		return c.observeHierarchy(ctx, r)
	}
	if r.Kind == "hierarchy-setting" {
		return c.observeHierarchySetting(ctx, r)
	}
	if r.Kind == "policy-list" {
		return c.observePolicyList(ctx, r)
	}
	if r.Kind != "" {
		return c.observeModern(ctx, r)
	}
	var list struct {
		Data *[]struct {
			ID   string `json:"templateId"`
			Name string `json:"templateName"`
			Type string `json:"templateType"`
		} `json:"data"`
	}
	if err := c.Request(ctx, "GET", "/dataservice/template/feature", nil, &list); err != nil {
		return nil, err
	}
	if list.Data == nil {
		return nil, ErrResponse
	}
	var id string
	for _, item := range *list.Data {
		if item.Name == r.Name {
			if id != "" || item.Type != r.Type || !validID.MatchString(item.ID) {
				return nil, errors.New("SD-WAN resource identity conflict")
			}
			id = item.ID
		}
	}
	if id == "" {
		return nil, nil
	}
	var body map[string]any
	if err := c.Request(ctx, "GET", "/dataservice/template/feature/object/"+url.PathEscape(id), nil, &body); err != nil {
		return nil, err
	}
	if body["templateName"] != r.Name || body["templateType"] != r.Type {
		return nil, ErrResponse
	}
	// Updating an attached template requires a separate device deployment workflow.
	attached := false
	for _, key := range []string{"attachedMastersCount", "devicesAttached"} {
		v, ok := body[key].(float64)
		if !ok || v < 0 {
			return nil, ErrResponse
		}
		if v > 0 {
			attached = true
		}
	}
	return &Observation{ID: id, Body: body, Attached: attached}, nil
}
func (c *Client) Create(ctx context.Context, r Resource) (string, error) {
	if r.Kind == "localized-policy" || r.Kind == "centralized-policy" || r.Kind == "security-policy" {
		return c.createLocalizedPolicy(ctx, r)
	}
	if r.Kind == "classic-definition" {
		return c.createClassicDefinition(ctx, r)
	}
	if r.Kind == "custom-app" {
		return c.createCustomApplication(ctx, r)
	}
	if r.Kind != "" {
		return c.createModern(ctx, r)
	}
	var result struct {
		ID string `json:"templateId"`
	}
	if err := c.Request(ctx, "POST", "/dataservice/template/feature", r.Body, &result); err != nil {
		return "", err
	}
	if !validID.MatchString(result.ID) {
		return "", ErrResponse
	}
	return result.ID, nil
}
func (c *Client) Update(ctx context.Context, r Resource, o *Observation) error {
	if o == nil || !validID.MatchString(o.ID) || o.Attached {
		return errors.New("SD-WAN update requires a qualified unattached resource")
	}
	if r.Kind != "" {
		if r.Kind == "localized-policy" || r.Kind == "centralized-policy" || r.Kind == "security-policy" {
			return c.updateLocalizedPolicy(ctx, r, o)
		}
		if r.Kind == "classic-definition" {
			return c.updateClassicDefinition(ctx, r, o)
		}
		if r.Kind == "custom-app" {
			return c.updateCustomApplication(ctx, r, o)
		}
		return c.updateModern(ctx, r, o)
	}
	return c.Request(ctx, "PUT", "/dataservice/template/feature/"+url.PathEscape(o.ID), r.Body, nil)
}
func (c *Client) DeleteFixture(ctx context.Context, id string) error {
	if !validID.MatchString(id) {
		return errors.New("invalid SD-WAN resource identity")
	}
	return c.Request(ctx, "DELETE", "/dataservice/template/feature/"+url.PathEscape(id), nil, nil)
}
