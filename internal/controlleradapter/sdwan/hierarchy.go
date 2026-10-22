// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

type NetworkHierarchy struct {
	Groups     []HierarchyContainer `json:"groups,omitempty"`
	Regions    []HierarchyContainer `json:"regions,omitempty"`
	Sites      []HierarchySite      `json:"sites,omitempty"`
	Collectors *HierarchyCollectors `json:"collectors,omitempty"`
}
type HierarchyContainer struct {
	Name        string               `json:"name"`
	Description *string              `json:"description,omitempty"`
	IsSecondary *bool                `json:"is_secondary,omitempty"`
	Groups      []HierarchyContainer `json:"groups,omitempty"`
	Regions     []HierarchyContainer `json:"regions,omitempty"`
	Sites       []HierarchySite      `json:"sites,omitempty"`
}
type HierarchySite struct {
	Name        string   `json:"name"`
	Description *string  `json:"description,omitempty"`
	SiteID      uint32   `json:"site_id"`
	Location    string   `json:"location,omitempty"`
	Latitude    *float64 `json:"latitude,omitempty"`
	Longitude   *float64 `json:"longitude,omitempty"`
}

const hierarchyPath = "/dataservice/v1/network-hierarchy"

func compileHierarchy(h *NetworkHierarchy) ([]Resource, error) {
	if h == nil {
		return nil, nil
	}
	out := []Resource{}
	names := map[string]bool{"Global": true}
	sites := map[uint32]bool{}
	add := func(name, parent, path, kind string, description *string, data map[string]any) error {
		if name == "" || len(name) > 128 || strings.TrimSpace(name) != name || strings.Contains(name, "/") || names[name] {
			return errors.New("invalid or duplicate hierarchy name")
		}
		names[name] = true
		body := map[string]any{"name": name, "data": data}
		if description != nil {
			body["description"] = *description
		}
		out = append(out, Resource{Key: "network_hierarchy/" + path + name, Name: name, Section: "network_hierarchy", Type: kind, Kind: "hierarchy", Collection: hierarchyPath, ProfileRefs: map[string]string{"parent": parent}, Body: body})
		return nil
	}
	addSites := func(items []HierarchySite, parent, path string) error {
		for _, s := range items {
			if s.SiteID == 0 || sites[s.SiteID] {
				return errors.New("invalid or duplicate hierarchy site_id")
			}
			sites[s.SiteID] = true
			location := s.Location
			if location == "" {
				location = "Undisclosed"
			}
			data := map[string]any{"label": "SITE", "location": location, "hierarchyId": map[string]any{"siteId": s.SiteID}}
			if location != "Undisclosed" && (s.Latitude == nil || s.Longitude == nil) {
				return errors.New("site location requires latitude and longitude")
			}
			gps := map[string]any{}
			if s.Latitude != nil {
				if *s.Latitude < -90 || *s.Latitude > 90 {
					return errors.New("invalid latitude")
				}
				gps["latitude"] = *s.Latitude
			}
			if s.Longitude != nil {
				if *s.Longitude < -180 || *s.Longitude > 180 {
					return errors.New("invalid longitude")
				}
				gps["longitude"] = *s.Longitude
			}
			if len(gps) > 0 {
				data["gpsLocation"] = gps
			}
			if e := add(s.Name, parent, path, "site", s.Description, data); e != nil {
				return e
			}
		}
		return nil
	}
	var walk func([]HierarchyContainer, []HierarchyContainer, string, string, int) error
	walk = func(groups, regions []HierarchyContainer, parent, path string, depth int) error {
		if depth > 2 && (len(groups) > 0 || len(regions) > 0) {
			return errors.New("hierarchy exceeds upstream depth")
		}
		for _, kind := range []string{"group", "region"} {
			items := groups
			if kind == "region" {
				items = regions
			}
			for _, n := range items {
				data := map[string]any{}
				if kind == "region" {
					data["label"] = "REGION"
					data["isSecondary"] = n.IsSecondary != nil && *n.IsSecondary
					if len(n.Regions) > 0 {
						return errors.New("regions cannot directly contain regions")
					}
				} else if n.IsSecondary != nil {
					return errors.New("is_secondary requires a region")
				}
				if e := add(n.Name, parent, path, kind, n.Description, data); e != nil {
					return e
				}
				next := path + n.Name + "/"
				if e := walk(n.Groups, n.Regions, n.Name, next, depth+1); e != nil {
					return e
				}
				if e := addSites(n.Sites, n.Name, next); e != nil {
					return e
				}
			}
		}
		return nil
	}
	if e := walk(h.Groups, h.Regions, "Global", "", 0); e != nil {
		return nil, e
	}
	if e := addSites(h.Sites, "Global", ""); e != nil {
		return nil, e
	}
	return out, nil
}
func (c *Client) hierarchy(ctx context.Context) ([]map[string]any, error) {
	var all []map[string]any
	for offset := 0; offset < 15000; offset += 1500 {
		var page []map[string]any
		if e := c.Request(ctx, "GET", fmt.Sprintf("%s?offset=%d&limit=1500", hierarchyPath, offset), nil, &page); e != nil {
			return nil, e
		}
		if page == nil {
			return nil, ErrResponse
		}
		all = append(all, page...)
		if len(page) < 1500 {
			return all, nil
		}
	}
	return nil, errors.New("hierarchy exceeds observation limit")
}
func (c *Client) resolveHierarchy(ctx context.Context, r Resource) ([]map[string]any, error) {
	nodes, e := c.hierarchy(ctx)
	if e != nil {
		return nil, e
	}
	id := ""
	for _, n := range nodes {
		if n["name"] == r.ProfileRefs["parent"] {
			data, _ := n["data"].(map[string]any)
			if data["label"] == "SITE" || r.Type == "region" && data["label"] == "REGION" {
				return nil, ErrResponse
			}
			v, ok := n["id"].(string)
			if !ok || !validID.MatchString(v) || id != "" {
				return nil, ErrResponse
			}
			id = v
		}
	}
	if id == "" {
		return nil, ErrDependencyPending
	}
	r.Body["data"].(map[string]any)["parentUuid"] = id
	return nodes, nil
}
func (c *Client) observeHierarchy(ctx context.Context, r Resource) (*Observation, error) {
	nodes, e := c.resolveHierarchy(ctx, r)
	if errors.Is(e, ErrDependencyPending) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	id := ""
	for _, n := range nodes {
		if n["name"] == r.Name {
			v, ok := n["id"].(string)
			if !ok || !validID.MatchString(v) || id != "" {
				return nil, ErrResponse
			}
			id = v
		}
	}
	if id == "" {
		return nil, nil
	}
	var body map[string]any
	if e = c.Request(ctx, "GET", hierarchyPath+"/"+id, nil, &body); e != nil {
		return nil, e
	}
	return &Observation{ID: id, Body: body}, nil
}
func equalHierarchy(r Resource, body map[string]any) (bool, error) {
	if body["name"] != r.Name {
		return false, nil
	}
	observed, ok := body["data"].(map[string]any)
	if !ok {
		return false, ErrResponse
	}
	// Generated IDs and server bookkeeping do not participate in desired intent.
	data := r.Body["data"].(map[string]any)
	for key, want := range data {
		a, _ := json.Marshal(want)
		b, _ := json.Marshal(observed[key])
		if string(a) != string(b) {
			return false, nil
		}
	}
	want, has := r.Body["description"]
	if has && !reflect.DeepEqual(want, body["description"]) {
		return false, nil
	}
	label := observed["label"]
	if r.Type == "group" && (label == "SITE" || label == "REGION") {
		return false, nil
	}
	return true, nil
}
func (c *Client) createHierarchy(ctx context.Context, r Resource) (string, error) {
	if _, e := c.resolveHierarchy(ctx, r); e != nil {
		return "", e
	}
	var result struct {
		Created []struct {
			UUID string `json:"uuid"`
		} `json:"created"`
	}
	if e := c.Request(ctx, "POST", hierarchyPath+"/", r.Body, &result); e != nil {
		return "", e
	}
	if len(result.Created) != 1 || !validID.MatchString(result.Created[0].UUID) {
		return "", ErrResponse
	}
	return result.Created[0].UUID, nil
}
func (c *Client) updateHierarchy(ctx context.Context, r Resource, o *Observation) error {
	if _, e := c.resolveHierarchy(ctx, r); e != nil {
		return e
	}
	data := r.Body["data"].(map[string]any)
	observed, ok := o.Body["data"].(map[string]any)
	if !ok {
		return ErrResponse
	}
	if r.Type == "site" {
		a, _ := json.Marshal(data["hierarchyId"])
		b, _ := json.Marshal(observed["hierarchyId"])
		if string(a) != string(b) {
			return errors.New("site_id cannot be modified")
		}
	}
	if r.Type == "region" {
		data["hierarchyId"] = observed["hierarchyId"]
	}
	return c.Request(ctx, "PUT", hierarchyPath+"/"+o.ID, r.Body, nil)
}
