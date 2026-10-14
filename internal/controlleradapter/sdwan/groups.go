// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"errors"
	"reflect"
	"sort"
)

type ConfigurationGroup struct {
	Name             string `json:"name"`
	Description      string `json:"description,omitempty"`
	SystemProfile    string `json:"system_profile,omitempty"`
	TransportProfile string `json:"transport_profile,omitempty"`
	ServiceProfile   string `json:"service_profile,omitempty"`
	CLIProfile       string `json:"cli_profile,omitempty"`
	OtherProfile     string `json:"other_profile,omitempty"`
}

type TopologyGroup struct {
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	TopologyProfile string `json:"topology_profile"`
}

func compileTopologyGroups(groups []TopologyGroup, profiles []Resource) ([]Resource, error) {
	known := map[string]bool{}
	for _, p := range profiles {
		if p.Kind == "profile" && p.Type == "topology" {
			known[p.Name] = true
		}
	}
	seen := map[string]bool{}
	out := make([]Resource, 0, len(groups))
	for _, g := range groups {
		if g.Name == "" || len(g.Name) > 128 || seen[g.Name] || !known[g.TopologyProfile] {
			return nil, errors.New("invalid topology group or undeclared topology profile")
		}
		seen[g.Name] = true
		out = append(out, Resource{Key: "topology_groups/" + g.Name, Name: g.Name, Section: "topology_groups", Kind: "group", Type: "topology_group", Collection: "/dataservice/v1/topology-group", ProfileRefs: map[string]string{"topology": g.TopologyProfile}, Body: map[string]any{"name": g.Name, "description": g.Description, "solution": "sdwan", "profiles": []any{}}})
	}
	return out, nil
}

func compileGroups(groups []ConfigurationGroup, profiles []Resource) ([]Resource, error) {
	known := map[string]bool{}
	for _, p := range profiles {
		if p.Kind == "profile" {
			known[p.Type+"/"+p.Name] = true
		}
	}
	seen := map[string]bool{}
	out := []Resource{}
	for _, g := range groups {
		if g.Name == "" || len(g.Name) > 128 || seen[g.Name] {
			return nil, errors.New("invalid or duplicate configuration group")
		}
		seen[g.Name] = true
		refs := map[string]string{}
		for kind, name := range map[string]string{"system": g.SystemProfile, "transport": g.TransportProfile, "service": g.ServiceProfile, "cli": g.CLIProfile, "other": g.OtherProfile} {
			if name != "" {
				if !known[kind+"/"+name] {
					return nil, errors.New("configuration group references an undeclared profile")
				}
				refs[kind] = name
			}
		}
		out = append(out, Resource{Key: "configuration_groups/" + g.Name, Name: g.Name, Section: "configuration_groups", Kind: "group", Type: "configuration_group", Collection: "/dataservice/v1/config-group", ProfileRefs: refs, Body: map[string]any{"name": g.Name, "description": g.Description, "solution": "sdwan", "profiles": []any{}}})
	}
	return out, nil
}
func (c *Client) resolveGroup(ctx context.Context, r Resource) error {
	kinds := []string{}
	for k := range r.ProfileRefs {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	refs := []any{}
	for _, kind := range kinds {
		id, e := c.profileID(ctx, "/dataservice/v1/feature-profile/sdwan/"+kind, r.ProfileRefs[kind])
		if e != nil {
			return e
		}
		if id == "" {
			return ErrDependencyPending
		}
		refs = append(refs, map[string]any{"id": id})
	}
	r.Body["profiles"] = refs
	return nil
}
func (c *Client) observeGroup(ctx context.Context, r Resource) (*Observation, error) {
	// Resolve references separately from group existence; a pre-existing group is
	// still observed when a declared parent is absent, so it cannot be adopted.
	if e := c.resolveGroup(ctx, r); e != nil && !errors.Is(e, ErrDependencyPending) {
		return nil, e
	}
	var list []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if e := c.Request(ctx, "GET", r.Collection, nil, &list); e != nil {
		return nil, e
	}
	if list == nil {
		return nil, ErrResponse
	}
	id := ""
	for _, x := range list {
		if x.Name == r.Name {
			if id != "" || !validID.MatchString(x.ID) {
				return nil, ErrResponse
			}
			id = x.ID
		}
	}
	if id == "" {
		return nil, nil
	}
	var body map[string]any
	if e := c.Request(ctx, "GET", r.Collection+"/"+id, nil, &body); e != nil {
		return nil, e
	}
	if r.Type == "topology_group" {
		return &Observation{ID: id, Body: body}, nil
	}
	var associated struct {
		Devices *[]any `json:"devices"`
	}
	if e := c.Request(ctx, "GET", r.Collection+"/"+id+"/device/associate", nil, &associated); e != nil {
		return nil, e
	}
	if associated.Devices == nil {
		return nil, ErrResponse
	}
	return &Observation{ID: id, Body: body, Attached: len(*associated.Devices) > 0}, nil
}
func equalGroup(r Resource, o map[string]any) (bool, error) {
	for _, k := range []string{"name", "description", "solution"} {
		if o[k] != r.Body[k] {
			return false, nil
		}
	}
	ids := func(v any) ([]string, error) {
		a, ok := v.([]any)
		if !ok {
			return nil, ErrResponse
		}
		result := []string{}
		for _, x := range a {
			m, ok := x.(map[string]any)
			if !ok {
				return nil, ErrResponse
			}
			id, ok := m["id"].(string)
			if !ok {
				return nil, ErrResponse
			}
			result = append(result, id)
		}
		sort.Strings(result)
		return result, nil
	}
	a, e := ids(r.Body["profiles"])
	if e != nil {
		return false, e
	}
	b, e := ids(o["profiles"])
	if e != nil {
		return false, e
	}
	return reflect.DeepEqual(a, b), nil
}
