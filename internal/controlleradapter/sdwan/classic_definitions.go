// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"reflect"
)

type LocalizedPolicies struct {
	Definitions LocalizedPolicyDefinitions `json:"definitions"`
}
type LocalizedPolicyDefinitions struct {
	IPv4AccessControlLists []ClassicACL `json:"ipv4_access_control_lists,omitempty"`
	IPv6AccessControlLists []ClassicACL `json:"ipv6_access_control_lists,omitempty"`
}
type ClassicACL struct {
	Name          string               `json:"name"`
	Description   string               `json:"description"`
	DefaultAction string               `json:"default_action"`
	Sequences     []ClassicACLSequence `json:"sequences"`
}
type ClassicACLSequence struct {
	ID            int             `json:"id"`
	Name          string          `json:"name,omitempty"`
	BaseAction    string          `json:"base_action"`
	MatchCriteria ClassicACLMatch `json:"match_criterias"`
}
type ClassicACLMatch struct {
	SourceIPPrefix      string `json:"source_ip_prefix,omitempty"`
	DestinationIPPrefix string `json:"destination_ip_prefix,omitempty"`
}

func compileLocalizedPolicies(input *LocalizedPolicies) ([]Resource, error) {
	if input == nil {
		return nil, nil
	}
	out := []Resource{}
	for _, spec := range []struct {
		family, apiType, path, modelType, sourceField, destinationField string
		items                                                           []ClassicACL
	}{{"ipv4", "acl", "acl/", "ipv4_acl_policy_definition", "sourceIp", "destinationIp", input.Definitions.IPv4AccessControlLists}, {"ipv6", "aclv6", "aclv6/", "ipv6_acl_policy_definition", "sourceIpv6", "destinationIpv6", input.Definitions.IPv6AccessControlLists}} {
		seen := map[string]bool{}
		for _, acl := range spec.items {
			if acl.Name == "" || len(acl.Name) > 128 || seen[acl.Name] || acl.Description == "" {
				return nil, errors.New("IPv4 ACL definition requires unique name and description")
			}
			seen[acl.Name] = true
			if acl.DefaultAction != "accept" && acl.DefaultAction != "drop" {
				return nil, errors.New("IPv4 ACL definition requires accept or drop default_action")
			}
			sequences := []any{}
			ids := map[int]bool{}
			for _, s := range acl.Sequences {
				if s.ID < 1 || s.ID > 65535 || ids[s.ID] || (s.BaseAction != "accept" && s.BaseAction != "drop") {
					return nil, errors.New("invalid IPv4 ACL sequence")
				}
				ids[s.ID] = true
				name := s.Name
				if name == "" {
					name = "Access Control List"
				}
				matches := []any{}
				if s.MatchCriteria.SourceIPPrefix != "" {
					prefix, err := netip.ParsePrefix(s.MatchCriteria.SourceIPPrefix)
					if err != nil || (spec.family == "ipv4" && !prefix.Addr().Is4()) || (spec.family == "ipv6" && !prefix.Addr().Is6()) {
						return nil, errors.New("invalid IPv4 ACL source_ip_prefix")
					}
					matches = append(matches, map[string]any{"field": spec.sourceField, "value": s.MatchCriteria.SourceIPPrefix})
				}
				if s.MatchCriteria.DestinationIPPrefix != "" {
					prefix, err := netip.ParsePrefix(s.MatchCriteria.DestinationIPPrefix)
					if err != nil || (spec.family == "ipv4" && !prefix.Addr().Is4()) || (spec.family == "ipv6" && !prefix.Addr().Is6()) {
						return nil, errors.New("invalid IPv4 ACL destination_ip_prefix")
					}
					matches = append(matches, map[string]any{"field": spec.destinationField, "value": s.MatchCriteria.DestinationIPPrefix})
				}
				sequence := map[string]any{"sequenceId": s.ID, "sequenceName": name, "sequenceType": spec.apiType, "baseAction": s.BaseAction, "match": map[string]any{"entries": matches}, "actions": []any{}}
				if spec.family == "ipv6" {
					sequence["sequenceIpType"] = "ipv6"
				}
				sequences = append(sequences, sequence)
			}
			body := map[string]any{"type": spec.apiType, "name": acl.Name, "description": acl.Description, "defaultAction": map[string]any{"type": acl.DefaultAction}, "sequences": sequences}
			out = append(out, Resource{Key: "localized_policies/definitions/" + spec.family + "_access_control_lists/" + acl.Name, Name: acl.Name, Section: "localized_policies", Kind: "classic-definition", Type: spec.modelType, Collection: "/dataservice/template/policy/definition/" + spec.path, Body: body})
		}
	}
	return out, nil
}
func (c *Client) observeClassicDefinition(ctx context.Context, r Resource) (*Observation, error) {
	var list struct {
		Data *[]struct {
			ID   string `json:"definitionId"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"data"`
	}
	if e := c.Request(ctx, "GET", r.Collection, nil, &list); e != nil {
		return nil, e
	}
	if list.Data == nil {
		return nil, ErrResponse
	}
	id := ""
	for _, item := range *list.Data {
		if item.Name == r.Name {
			if id != "" || item.Type != r.Body["type"] || !validID.MatchString(item.ID) {
				return nil, ErrResponse
			}
			id = item.ID
		}
	}
	if id == "" {
		return nil, nil
	}
	var body map[string]any
	if e := c.Request(ctx, "GET", r.Collection+url.PathEscape(id), nil, &body); e != nil {
		return nil, e
	}
	return &Observation{ID: id, Body: body}, nil
}
func (c *Client) createClassicDefinition(ctx context.Context, r Resource) (string, error) {
	var response map[string]any
	if e := c.Request(ctx, "POST", r.Collection, r.Body, &response); e != nil {
		return "", e
	}
	id, _ := response["definitionId"].(string)
	if !validID.MatchString(id) {
		return "", ErrResponse
	}
	return id, nil
}
func (c *Client) updateClassicDefinition(ctx context.Context, r Resource, o *Observation) error {
	return c.Request(ctx, "PUT", r.Collection+url.PathEscape(o.ID), r.Body, nil)
}
func (c *Client) deleteClassicDefinition(ctx context.Context, r Resource, id string) error {
	if !validID.MatchString(id) {
		return ErrResponse
	}
	return c.Request(ctx, "DELETE", r.Collection+url.PathEscape(id), nil, nil)
}
func equalClassicDefinition(r Resource, o map[string]any) (bool, error) {
	if o["name"] != r.Name || o["type"] != r.Body["type"] {
		return false, nil
	}
	if description, _ := o["description"].(string); description != r.Body["description"] {
		return false, nil
	}
	actual := map[string]any{"type": o["type"], "name": o["name"], "description": o["description"], "defaultAction": o["defaultAction"], "sequences": o["sequences"]}
	if actual["sequences"] == nil {
		actual["sequences"] = []any{}
	}
	if action, ok := actual["defaultAction"].(map[string]any); ok {
		actual["defaultAction"] = map[string]any{"type": action["type"]}
	}
	if rows, ok := actual["sequences"].([]any); ok {
		normalized := []any{}
		for _, row := range rows {
			entry, ok := row.(map[string]any)
			if !ok {
				return false, ErrResponse
			}
			sequence := map[string]any{"sequenceId": entry["sequenceId"], "sequenceName": entry["sequenceName"], "sequenceType": entry["sequenceType"], "baseAction": entry["baseAction"], "match": entry["match"], "actions": entry["actions"]}
			if r.Type == "ipv6_acl_policy_definition" {
				sequence["sequenceIpType"] = entry["sequenceIpType"]
			}
			normalized = append(normalized, sequence)
		}
		actual["sequences"] = normalized
	}
	// Manager may add version and timestamps; compare only the modeled body.
	wanted, err := json.Marshal(r.Body)
	if err != nil {
		return false, ErrResponse
	}
	var normalized map[string]any
	if json.Unmarshal(wanted, &normalized) != nil {
		return false, ErrResponse
	}
	return reflect.DeepEqual(normalized, actual), nil
}
