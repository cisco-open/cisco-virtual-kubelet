// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"errors"
	"net/url"
	"strings"
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
	parcel := r.ParcelPath
	if parcel == "" {
		parcel = "banner"
	}
	ancestors := r.ParcelAncestors
	if len(ancestors) == 0 && r.ParentParcelPath != "" {
		ancestors = []ParcelAncestor{{Path: r.ParentParcelPath, Name: r.ParentParcelName}}
	}
	path := r.Collection + "/" + url.PathEscape(id)
	for _, ancestor := range ancestors {
		if ancestor.Path == "" || ancestor.Name == "" {
			return "", errors.New("nested parcel requires parent path and name")
		}
		parentPath := path + "/" + ancestor.Path
		var result struct {
			Data []struct {
				ID      string `json:"parcelId"`
				Payload struct {
					Name string `json:"name"`
				} `json:"payload"`
				Name string `json:"name"`
			} `json:"data"`
		}
		if e := c.Request(ctx, "GET", parentPath, nil, &result); e != nil {
			return "", e
		}
		if result.Data == nil {
			return "", ErrResponse
		}
		parentID := ""
		for _, item := range result.Data {
			if item.Name == ancestor.Name || item.Payload.Name == ancestor.Name {
				if parentID != "" || !validID.MatchString(item.ID) {
					return "", ErrResponse
				}
				parentID = item.ID
			}
		}
		if parentID == "" {
			return "", ErrDependencyPending
		}
		path = parentPath + "/" + url.PathEscape(parentID)
	}
	return path + "/" + parcel, nil
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
	} else if r.Kind == "policy-parcel" {
		var profile struct {
			Count   *int `json:"profileParcelCount"`
			Parcels []struct {
				ID      string `json:"parcelId"`
				Type    string `json:"parcelType"`
				Payload struct {
					Name string `json:"name"`
				} `json:"payload"`
			} `json:"associatedProfileParcels"`
		}
		if e = c.Request(ctx, "GET", strings.TrimSuffix(path, "/"+r.ParcelPath), nil, &profile); e != nil {
			return nil, e
		}
		if profile.Parcels == nil && (profile.Count == nil || *profile.Count != 0) {
			return nil, ErrResponse
		}
		for _, p := range profile.Parcels {
			if p.Payload.Name == r.Name {
				if id != "" || p.Type != r.ParcelPath || !validID.MatchString(p.ID) {
					return nil, ErrResponse
				}
				id = p.ID
			}
		}
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
	if e = c.Request(ctx, "GET", strings.TrimSuffix(path, "/")+"/"+url.PathEscape(id), nil, &body); e != nil {
		return nil, e
	}
	if e := c.resolveParcelRefs(ctx, r); e != nil {
		return nil, e
	}
	if e := c.resolveMeshSites(ctx, r); e != nil {
		return nil, e
	}
	if e := c.resolveCustomControlSites(ctx, r); e != nil {
		return nil, e
	}
	if e := c.resolveHubSpokeSites(ctx, r); e != nil {
		return nil, e
	}
	return &Observation{ID: id, Body: body}, nil
}
func (c *Client) createModern(ctx context.Context, r Resource) (string, error) {
	if r.Kind == "cloud-setting" {
		return c.createCloudSettings(ctx, r)
	}
	if r.Kind == "hierarchy-setting" {
		return c.createHierarchySetting(ctx, r)
	}
	if e := c.resolveParcelRefs(ctx, r); e != nil {
		return "", e
	}
	if e := c.resolveMeshSites(ctx, r); e != nil {
		return "", e
	}
	if e := c.resolveCustomControlSites(ctx, r); e != nil {
		return "", e
	}
	if e := c.resolveHubSpokeSites(ctx, r); e != nil {
		return "", e
	}
	if r.Kind == "hierarchy" {
		return c.createHierarchy(ctx, r)
	}
	if e := c.resolvePolicyRefs(ctx, r); e != nil {
		return "", e
	}
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
	if r.Kind == "policy-list" {
		key = "listId"
	}
	if r.Kind == "parcel" || r.Kind == "association" {
		key = "parcelId"
	}
	id, ok := result[key].(string)
	if r.Kind == "policy-parcel" && (!ok || !validID.MatchString(id)) {
		id, ok = result["parcelId"].(string)
	}
	if !ok || !validID.MatchString(id) {
		return "", ErrResponse
	}
	return id, nil
}
func (c *Client) updateModern(ctx context.Context, r Resource, o *Observation) error {
	if r.Kind == "cloud-setting" {
		return c.updateCloudSettings(ctx, r)
	}
	if r.Kind == "hierarchy-setting" {
		return c.updateHierarchySetting(ctx, r, o)
	}
	if e := c.resolveParcelRefs(ctx, r); e != nil {
		return e
	}
	if e := c.resolveMeshSites(ctx, r); e != nil {
		return e
	}
	if e := c.resolveCustomControlSites(ctx, r); e != nil {
		return e
	}
	if e := c.resolveHubSpokeSites(ctx, r); e != nil {
		return e
	}
	if r.Kind == "association" && o != nil && o.ID == r.Body["parcelId"] {
		return nil
	}
	if r.Kind == "hierarchy" {
		return c.updateHierarchy(ctx, r, o)
	}
	if e := c.resolvePolicyRefs(ctx, r); e != nil {
		return e
	}
	if r.Kind == "group" {
		if e := c.resolveGroup(ctx, r); e != nil {
			return e
		}
	}
	path, e := c.collection(ctx, r)
	if e != nil {
		return e
	}
	return c.Request(ctx, "PUT", strings.TrimSuffix(path, "/")+"/"+url.PathEscape(o.ID), r.Body, nil)
}
func (c *Client) DeleteResourceFixture(ctx context.Context, r Resource, id string) error {
	if r.Kind == "cloud-setting" {
		return nil
	} // Manager has no delete endpoint for this singleton.
	if r.Kind == "hierarchy-setting" {
		return c.deleteHierarchySetting(ctx, r, id)
	}
	if r.Kind == "localized-policy" || r.Kind == "centralized-policy" || r.Kind == "security-policy" {
		return c.deleteLocalizedPolicy(ctx, r, id)
	}
	if r.Kind == "classic-definition" {
		return c.deleteClassicDefinition(ctx, r, id)
	}
	if r.Kind == "" {
		return c.DeleteFixture(ctx, id)
	}
	if !validID.MatchString(id) {
		return ErrResponse
	}
	if r.Kind == "custom-app" {
		return c.deleteCustomApplication(ctx, r, id)
	}
	path, e := c.collection(ctx, r)
	if e != nil {
		return e
	}
	return c.Request(ctx, "DELETE", strings.TrimSuffix(path, "/")+"/"+url.PathEscape(id), nil, nil)
}
