// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed definitions/topology-mesh-schema.json
var topologyMeshSchema []byte

func compileMeshPolicies(p FeatureProfile, collection, key string) ([]Resource, error) {
	if len(p.MeshPolicies) == 0 {
		return nil, nil
	}
	if collection != "topology" {
		return nil, errors.New("mesh policies require a topology profile")
	}
	out := []Resource{}
	seen := map[string]bool{}
	for _, input := range p.MeshPolicies {
		for field := range input {
			switch field {
			case "name", "description", "lan_vpn_names", "sites", "site_groups":
			default:
				return nil, fmt.Errorf("unsupported mesh policy field %s", field)
			}
		}
		name, ok := input["name"].(string)
		if !ok || name == "" || len(name) > 128 || seen[name] {
			return nil, errors.New("invalid or duplicate mesh policy name")
		}
		seen[name] = true
		vpns, err := meshStrings(input["lan_vpn_names"], true)
		if err != nil {
			return nil, fmt.Errorf("mesh lan_vpn_names: %w", err)
		}
		sites, err := meshStrings(input["sites"], false)
		if err != nil {
			return nil, fmt.Errorf("mesh sites: %w", err)
		}
		groups, err := meshStrings(input["site_groups"], false)
		if err != nil {
			return nil, fmt.Errorf("mesh site_groups: %w", err)
		}
		out = append(out, Resource{Key: key + "/mesh_policies/" + name, Name: name, Section: "feature_profiles", Kind: "policy-parcel", Type: "topology_mesh", Collection: "/dataservice/v1/feature-profile/sdwan/topology", ParentName: p.Name, ParcelPath: "mesh", HierarchySites: sites, SiteGroups: groups, Body: map[string]any{"name": name, "description": "", "data": map[string]any{"target": map[string]any{"vpn": map[string]any{"optionType": "global", "value": vpns}}}}})
	}
	return out, nil
}

func meshStrings(value any, required bool) ([]string, error) {
	if value == nil && !required {
		return []string{}, nil
	}
	items, ok := value.([]any)
	if !ok || (required && len(items) == 0) {
		return nil, errors.New("nonempty string array required")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, item := range items {
		name, ok := item.(string)
		if !ok || name == "" || seen[name] {
			return nil, errors.New("invalid or duplicate entry")
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

func prepareMeshPolicies(resources []Resource, managerVersion string) error {
	useUUID := true
	if managerVersion != "" {
		parts := strings.Split(managerVersion, ".")
		major, err := strconv.Atoi(parts[0])
		if err != nil {
			return errors.New("invalid manager_version")
		}
		minor := 0
		if len(parts) > 1 {
			minor, err = strconv.Atoi(parts[1])
			if err != nil {
				return errors.New("invalid manager_version")
			}
		}
		useUUID = major > 20 || major == 20 && minor >= 18
	}
	parents := map[string]string{}
	siteNames := map[string]bool{}
	for _, r := range resources {
		if r.Kind == "hierarchy" {
			parents[r.Name] = r.ProfileRefs["parent"]
			if r.Type == "site" {
				siteNames[r.Name] = true
			}
		}
	}
	orderedSites := make([]string, 0, len(siteNames))
	for site := range siteNames {
		orderedSites = append(orderedSites, site)
	}
	sort.Strings(orderedSites)
	for i := range resources {
		r := &resources[i]
		if r.Type != "topology_mesh" {
			continue
		}
		selected := append([]string{}, r.HierarchySites...)
		for _, group := range r.SiteGroups {
			if _, ok := parents[group]; !ok || siteNames[group] {
				return errors.New("mesh site group is not declared")
			}
			for _, site := range orderedSites {
				for parent := parents[site]; parent != ""; parent = parents[parent] {
					if parent == group {
						selected = append(selected, site)
						break
					}
				}
			}
		}
		unique := []string{}
		seen := map[string]bool{}
		for _, site := range selected {
			if !seen[site] {
				unique = append(unique, site)
				seen[site] = true
			}
		}
		if len(unique) == 0 {
			return errors.New("mesh policy resolves to no sites")
		}
		r.HierarchySites = unique
		data := r.Body["data"].(map[string]any)
		values := []string{}
		for _, site := range unique {
			if useUUID && len(siteNames) > 0 {
				if !siteNames[site] {
					return errors.New("mesh site is not declared in network_hierarchy")
				}
				values = append(values, parcelPlaceholder(site))
			} else {
				values = append(values, site)
			}
		}
		if useUUID && len(siteNames) > 0 {
			data["hierarchyUuid"] = map[string]any{"optionType": "global", "value": values}
		} else {
			data["sites"] = map[string]any{"optionType": "global", "value": values}
			declared := []string{}
			for _, site := range unique {
				if siteNames[site] {
					declared = append(declared, site)
				}
			}
			r.HierarchySites = declared
		}
		var schema map[string]any
		encoded, _ := json.Marshal(data)
		var normalized any
		if json.Unmarshal(topologyMeshSchema, &schema) != nil || json.Unmarshal(encoded, &normalized) != nil || !validScalarValue(normalized, schema) {
			return errors.New("mesh intent violates provider schema")
		}
	}
	return nil
}

func (c *Client) resolveMeshSites(ctx context.Context, r Resource) error {
	if r.Type != "topology_mesh" || len(r.HierarchySites) == 0 || r.Body["data"].(map[string]any)["hierarchyUuid"] == nil {
		return nil
	}
	nodes, err := c.hierarchy(ctx)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(r.HierarchySites))
	for _, site := range r.HierarchySites {
		id := ""
		for _, node := range nodes {
			if node["name"] == site {
				data, _ := node["data"].(map[string]any)
				if data["label"] != "SITE" || id != "" {
					return ErrResponse
				}
				id, _ = node["id"].(string)
			}
		}
		if id == "" {
			return ErrDependencyPending
		}
		if !validID.MatchString(id) {
			return ErrResponse
		}
		ids = append(ids, id)
	}
	r.Body["data"].(map[string]any)["hierarchyUuid"].(map[string]any)["value"] = ids
	return nil
}
