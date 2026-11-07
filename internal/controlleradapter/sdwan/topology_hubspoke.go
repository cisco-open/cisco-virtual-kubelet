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

//go:embed definitions/topology-hubspoke-schema.json
var topologyHubSpokeSchema []byte

type hubSpokeInput struct {
	Selected []string
	Spokes   []hubSpokeGroup
	UUIDMode bool
}
type hubSpokeGroup struct {
	Name              string
	Sites, SiteGroups []string
	Preferences       []hubPreference
}
type hubPreference struct {
	Sites      []string
	Preference any
}

func compileHubSpokePolicies(p FeatureProfile, collection, key string) ([]Resource, error) {
	if len(p.HubSpokePolicies) == 0 {
		return nil, nil
	}
	if collection != "topology" {
		return nil, errors.New("hub-spoke policies require a topology profile")
	}
	out := []Resource{}
	seen := map[string]bool{}
	for _, input := range p.HubSpokePolicies {
		for field := range input {
			switch field {
			case "name", "description", "lan_vpn_names", "selected_hub_sites", "spoke_groups":
			default:
				return nil, fmt.Errorf("unsupported hub-spoke policy field %s", field)
			}
		}
		name, ok := input["name"].(string)
		if !ok || name == "" || len(name) > 128 || seen[name] {
			return nil, errors.New("invalid or duplicate hub-spoke policy name")
		}
		seen[name] = true
		vpns, err := meshStrings(input["lan_vpn_names"], true)
		if err != nil {
			return nil, fmt.Errorf("hub-spoke lan_vpn_names: %w", err)
		}
		selected, err := meshStrings(input["selected_hub_sites"], true)
		if err != nil {
			return nil, fmt.Errorf("hub-spoke selected_hub_sites: %w", err)
		}
		intent := &hubSpokeInput{Selected: selected}
		if raw := input["spoke_groups"]; raw != nil {
			groups, ok := raw.([]any)
			if !ok {
				return nil, errors.New("hub-spoke spoke_groups must be an array")
			}
			for _, item := range groups {
				row, ok := item.(map[string]any)
				if !ok {
					return nil, errors.New("hub-spoke spoke group must be an object")
				}
				for field := range row {
					switch field {
					case "name", "spoke_sites", "spoke_site_groups", "hub_preferences":
					default:
						return nil, fmt.Errorf("unsupported hub-spoke group field %s", field)
					}
				}
				name, ok := row["name"].(string)
				if !ok || name == "" {
					return nil, errors.New("hub-spoke group name is required")
				}
				sites, err := meshStrings(row["spoke_sites"], false)
				if err != nil {
					return nil, err
				}
				siteGroups, err := meshStrings(row["spoke_site_groups"], false)
				if err != nil {
					return nil, err
				}
				spoke := hubSpokeGroup{Name: name, Sites: sites, SiteGroups: siteGroups}
				if rawPrefs := row["hub_preferences"]; rawPrefs != nil {
					prefs, ok := rawPrefs.([]any)
					if !ok {
						return nil, errors.New("hub_preferences must be an array")
					}
					for _, pref := range prefs {
						entry, ok := pref.(map[string]any)
						if !ok {
							return nil, errors.New("hub preference must be an object")
						}
						for field := range entry {
							switch field {
							case "hub_sites", "preference":
							default:
								return nil, fmt.Errorf("unsupported hub preference field %s", field)
							}
						}
						hubs, err := meshStrings(entry["hub_sites"], true)
						if err != nil {
							return nil, err
						}
						if v := entry["preference"]; v != nil {
							if _, ok := v.(float64); !ok {
								return nil, errors.New("hub preference must be a number")
							}
						}
						spoke.Preferences = append(spoke.Preferences, hubPreference{Sites: hubs, Preference: entry["preference"]})
					}
				}
				intent.Spokes = append(intent.Spokes, spoke)
			}
		}
		out = append(out, Resource{Key: key + "/hub_spoke_policies/" + name, Name: name, Section: "feature_profiles", Kind: "policy-parcel", Type: "topology_hubspoke", Collection: "/dataservice/v1/feature-profile/sdwan/topology", ParentName: p.Name, ParcelPath: "hubspoke", HubSpoke: intent, Body: map[string]any{"name": name, "description": "", "data": map[string]any{"target": map[string]any{"vpn": map[string]any{"optionType": "global", "value": vpns}}}}})
	}
	return out, nil
}

func (h *hubSpokeInput) names() []string {
	all := append([]string{}, h.Selected...)
	for _, spoke := range h.Spokes {
		all = append(all, spoke.Sites...)
		for _, pref := range spoke.Preferences {
			all = append(all, pref.Sites...)
		}
	}
	return all
}

func prepareHubSpokePolicies(resources []Resource, version string) error {
	useUUID := true
	if version != "" {
		parts := strings.Split(version, ".")
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
	useUUID = useUUID && len(siteNames) > 0
	orderedSites := make([]string, 0, len(siteNames))
	for site := range siteNames {
		orderedSites = append(orderedSites, site)
	}
	sort.Strings(orderedSites)
	expand := func(groups []string, sites []string) ([]string, error) {
		out := append([]string{}, sites...)
		for _, group := range groups {
			if _, ok := parents[group]; !ok || siteNames[group] {
				return nil, errors.New("hub-spoke site group is not declared")
			}
			for _, site := range orderedSites {
				for parent := parents[site]; parent != ""; parent = parents[parent] {
					if parent == group {
						out = append(out, site)
						break
					}
				}
			}
		}
		unique := []string{}
		seen := map[string]bool{}
		for _, site := range out {
			if !seen[site] {
				unique = append(unique, site)
				seen[site] = true
			}
		}
		if len(unique) == 0 {
			return nil, errors.New("hub-spoke site selection resolves to no sites")
		}
		if useUUID {
			for _, site := range unique {
				if !siteNames[site] {
					return nil, errors.New("hub-spoke site is not declared")
				}
			}
		}
		return unique, nil
	}
	opt := func(sites []string) map[string]any {
		values := []string{}
		for _, site := range sites {
			if useUUID {
				values = append(values, parcelPlaceholder(site))
			} else {
				values = append(values, site)
			}
		}
		return map[string]any{"optionType": "global", "value": values}
	}
	for i := range resources {
		r := &resources[i]
		if r.HubSpoke == nil {
			continue
		}
		h := r.HubSpoke
		h.UUIDMode = useUUID
		selected, err := expand(nil, h.Selected)
		if err != nil {
			return err
		}
		h.Selected = selected
		data := r.Body["data"].(map[string]any)
		if useUUID {
			data["selectedHierarchyHubs"] = opt(selected)
		} else {
			data["selectedHubs"] = opt(selected)
		}
		spokes := []any{}
		for j := range h.Spokes {
			spoke := &h.Spokes[j]
			sites, err := expand(spoke.SiteGroups, spoke.Sites)
			if err != nil {
				return err
			}
			spoke.Sites = sites
			body := map[string]any{"name": map[string]any{"optionType": "global", "value": spoke.Name}}
			if useUUID {
				body["spokeHierarchyUuid"] = opt(sites)
			} else {
				body["spokeSites"] = opt(sites)
			}
			hubSites := []any{}
			for k := range spoke.Preferences {
				pref := &spoke.Preferences[k]
				hubs, err := expand(nil, pref.Sites)
				if err != nil {
					return err
				}
				pref.Sites = hubs
				entry := map[string]any{}
				if useUUID {
					entry["hierarchyUuid"] = opt(hubs)
				} else {
					entry["sites"] = opt(hubs)
				}
				if pref.Preference != nil {
					entry["preference"] = map[string]any{"optionType": "global", "value": pref.Preference}
				}
				hubSites = append(hubSites, entry)
			}
			body["hubSites"] = hubSites
			spokes = append(spokes, body)
		}
		data["spokes"] = spokes
		var schema map[string]any
		encoded, _ := json.Marshal(data)
		var normalized any
		if json.Unmarshal(topologyHubSpokeSchema, &schema) != nil || json.Unmarshal(encoded, &normalized) != nil || !validScalarValue(normalized, schema) {
			return errors.New("hub-spoke intent violates provider schema")
		}
	}
	return nil
}

func (c *Client) resolveHubSpokeSites(ctx context.Context, r Resource) error {
	if r.HubSpoke == nil || !r.HubSpoke.UUIDMode {
		return nil
	}
	nodes, err := c.hierarchy(ctx)
	if err != nil {
		return err
	}
	ids := map[string]string{}
	for _, node := range nodes {
		data, _ := node["data"].(map[string]any)
		if data["label"] == "SITE" {
			name, _ := node["name"].(string)
			id, _ := node["id"].(string)
			if ids[name] != "" || !validID.MatchString(id) {
				return ErrResponse
			}
			ids[name] = id
		}
	}
	resolve := func(names []string) ([]string, error) {
		out := []string{}
		for _, name := range names {
			id := ids[name]
			if id == "" {
				return nil, ErrDependencyPending
			}
			out = append(out, id)
		}
		return out, nil
	}
	h := r.HubSpoke
	data := r.Body["data"].(map[string]any)
	selected, err := resolve(h.Selected)
	if err != nil {
		return err
	}
	data["selectedHierarchyHubs"].(map[string]any)["value"] = selected
	spokes := data["spokes"].([]any)
	for i, spoke := range h.Spokes {
		body := spokes[i].(map[string]any)
		siteIDs, err := resolve(spoke.Sites)
		if err != nil {
			return err
		}
		body["spokeHierarchyUuid"].(map[string]any)["value"] = siteIDs
		hubSites := body["hubSites"].([]any)
		for j, pref := range spoke.Preferences {
			hubIDs, err := resolve(pref.Sites)
			if err != nil {
				return err
			}
			hubSites[j].(map[string]any)["hierarchyUuid"].(map[string]any)["value"] = hubIDs
		}
	}
	return nil
}
