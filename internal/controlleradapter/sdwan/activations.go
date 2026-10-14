// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"errors"
	"net/url"
)

const centralizedPolicyCollection = "/dataservice/template/policy/vsmart/"

func (c *Client) centralizedPolicyID(ctx context.Context, name string) (string, error) {
	var list struct {
		Data *[]struct {
			ID   string `json:"policyId"`
			Name string `json:"policyName"`
			Type string `json:"policyType"`
		} `json:"data"`
	}
	if err := c.Request(ctx, "GET", centralizedPolicyCollection, nil, &list); err != nil {
		return "", err
	}
	if list.Data == nil {
		return "", ErrResponse
	}
	id := ""
	for _, item := range *list.Data {
		if item.Name == name {
			if id != "" || item.Type != "feature" || !validID.MatchString(item.ID) {
				return "", errors.New("centralized policy identity conflict")
			}
			id = item.ID
		}
	}
	return id, nil
}

func (c *Client) observeActivation(ctx context.Context, r Resource) (*Observation, error) {
	if r.Type != "centralized-policy" {
		return nil, errors.New("unsupported activation type")
	}
	id, err := c.centralizedPolicyID(ctx, r.Name)
	if err != nil || id == "" {
		return nil, err
	}
	var body map[string]any
	if err := c.Request(ctx, "GET", centralizedPolicyCollection+"definition/"+url.PathEscape(id), nil, &body); err != nil {
		return nil, err
	}
	active, ok := body["isPolicyActivated"].(bool)
	if !ok {
		return nil, ErrResponse
	}
	if !active {
		return nil, nil
	}
	return &Observation{ID: id, Body: map[string]any{"activated": true}}, nil
}

func (c *Client) createActivation(ctx context.Context, r Resource) (string, error) {
	if r.Type != "centralized-policy" {
		return "", errors.New("unsupported activation type")
	}
	id, err := c.centralizedPolicyID(ctx, r.Name)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", ErrDependencyPending
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := c.Request(ctx, "POST", centralizedPolicyCollection+"activate/"+url.PathEscape(id)+"?confirm=true", map[string]any{}, &result); err != nil {
		return "", err
	}
	if err := c.waitCustomApplicationTask(ctx, result.ID); err != nil {
		return "", err
	}
	return id, nil
}

func equalActivation(r Resource, observed map[string]any) (bool, error) {
	active, ok := observed["activated"].(bool)
	if !ok {
		return false, ErrResponse
	}
	return active && r.Body["activated"] == true, nil
}
