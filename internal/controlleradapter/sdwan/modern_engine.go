// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"errors"
	"net/url"
)

var ErrDependencyPending = errors.New("SD-WAN profile dependency is not yet present")

func (c *Client) profileID(ctx context.Context, collection, name string) (string, error) {
	var list []struct {
		ID   string `json:"profileId"`
		Name string `json:"profileName"`
	}
	if e := c.Request(ctx, "GET", collection, nil, &list); e != nil {
		return "", e
	}
	if list == nil {
		return "", ErrResponse
	}
	id := ""
	for _, item := range list {
		if item.Name == name {
			if id != "" || !validID.MatchString(item.ID) {
				return "", errors.New("SD-WAN profile identity conflict")
			}
			id = item.ID
		}
	}
	return id, nil
}
func (c *Client) collection(ctx context.Context, r Resource) (string, error) {
	if r.ParentName == "" {
		return r.Collection, nil
	}
	id, e := c.profileID(ctx, r.Collection, r.ParentName)
	if e != nil {
		return "", e
	}
	if id == "" {
		return "", ErrDependencyPending
	}
	return r.Collection + "/" + url.PathEscape(id) + "/banner", nil
}
func (c *Client) observeModern(ctx context.Context, r Resource) (*Observation, error) {
	path, e := c.collection(ctx, r)
	if r.Kind == "group" {
		return c.observeGroup(ctx, r)
	}
	if errors.Is(e, ErrDependencyPending) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	id := ""
	if r.Kind == "profile" {
		id, e = c.profileID(ctx, path, r.Name)
	} else {
		var result struct {
			Data []struct {
				ID      string `json:"parcelId"`
				Payload struct {
					Name string `json:"name"`
				} `json:"payload"`
				Name string `json:"name"`
			} `json:"data"`
		}
		if e = c.Request(ctx, "GET", path, nil, &result); e != nil {
			return nil, e
		}
		if result.Data == nil {
			return nil, ErrResponse
		}
		for _, item := range result.Data {
			if item.Payload.Name == r.Name || item.Name == r.Name {
				if id != "" || !validID.MatchString(item.ID) {
					return nil, ErrResponse
				}
				id = item.ID
			}
		}
	}
	if e != nil {
		return nil, e
	}
	if id == "" {
		return nil, nil
	}
	var body map[string]any
	if e = c.Request(ctx, "GET", path+"/"+url.PathEscape(id), nil, &body); e != nil {
		return nil, e
	}
	return &Observation{ID: id, Body: body}, nil
}
func (c *Client) createModern(ctx context.Context, r Resource) (string, error) {
	if r.Kind == "group" {
		if e := c.resolveGroup(ctx, r); e != nil {
			return "", e
		}
	}
	path, e := c.collection(ctx, r)
	if e != nil {
		return "", e
	}
	var result map[string]any
	if e = c.Request(ctx, "POST", path, r.Body, &result); e != nil {
		return "", e
	}
	key := "id"
	if r.Kind == "parcel" {
		key = "parcelId"
	}
	id, ok := result[key].(string)
	if !ok || !validID.MatchString(id) {
		return "", ErrResponse
	}
	return id, nil
}
func (c *Client) updateModern(ctx context.Context, r Resource, o *Observation) error {
	if r.Kind == "group" {
		if e := c.resolveGroup(ctx, r); e != nil {
			return e
		}
	}
	path, e := c.collection(ctx, r)
	if e != nil {
		return e
	}
	return c.Request(ctx, "PUT", path+"/"+url.PathEscape(o.ID), r.Body, nil)
}
func (c *Client) DeleteResourceFixture(ctx context.Context, r Resource, id string) error {
	if r.Kind == "" {
		return c.DeleteFixture(ctx, id)
	}
	if !validID.MatchString(id) {
		return ErrResponse
	}
	path, e := c.collection(ctx, r)
	if e != nil {
		return e
	}
	return c.Request(ctx, "DELETE", path+"/"+url.PathEscape(id), nil, nil)
}
