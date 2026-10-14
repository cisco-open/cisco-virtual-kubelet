// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
)

type ClassicDefinitionReference struct{ Type, Name string }

type LocalizedFeaturePolicy struct {
	Name                       string `json:"name"`
	Description                string `json:"description"`
	IPv4FlowVisibility         *bool  `json:"ipv4_flow_visibility,omitempty"`
	IPv6FlowVisibility         *bool  `json:"ipv6_flow_visibility,omitempty"`
	IPv4ApplicationVisibility  *bool  `json:"ipv4_application_visibility,omitempty"`
	IPv6ApplicationVisibility  *bool  `json:"ipv6_application_visibility,omitempty"`
	CloudQoS                   *bool  `json:"cloud_qos,omitempty"`
	CloudQoSServiceSide        *bool  `json:"cloud_qos_service_side,omitempty"`
	ImplicitACLLogging         *bool  `json:"implicit_acl_logging,omitempty"`
	LogFrequency               *int   `json:"log_frequency,omitempty"`
	IPv4VisibilityCacheEntries *int   `json:"ipv4_visibility_cache_entries,omitempty"`
	IPv6VisibilityCacheEntries *int   `json:"ipv6_visibility_cache_entries,omitempty"`
	Definitions                struct {
		QoSMaps                  []string `json:"qos_maps,omitempty"`
		RewriteRules             []string `json:"rewrite_rules,omitempty"`
		RoutePolicies            []string `json:"route_policies,omitempty"`
		IPv4AccessControlLists   []string `json:"ipv4_access_control_lists,omitempty"`
		IPv6AccessControlLists   []string `json:"ipv6_access_control_lists,omitempty"`
		IPv4DeviceAccessPolicies []string `json:"ipv4_device_access_policies,omitempty"`
		IPv6DeviceAccessPolicies []string `json:"ipv6_device_access_policies,omitempty"`
	} `json:"definitions"`
}

type classicDefinitionSpec struct{ path, apiType string }

var classicDefinitionTypes = map[string]classicDefinitionSpec{
	"qos_maps":                    {"qosmap/", "qosMap"},
	"rewrite_rules":               {"rewriterule/", "rewriteRule"},
	"route_policies":              {"vedgeroute/", "vedgeRoute"},
	"ipv4_access_control_lists":   {"acl/", "acl"},
	"ipv6_access_control_lists":   {"aclv6/", "aclv6"},
	"ipv4_device_access_policies": {"deviceaccesspolicy/", "deviceAccessPolicy"},
	"ipv6_device_access_policies": {"deviceaccesspolicyv6/", "deviceAccessPolicyv6"},
	"centralized_policies/definitions/control_policy/mesh_topology":          {"mesh/", "mesh"},
	"centralized_policies/definitions/control_policy/hub_and_spoke_topology": {"hubandspoke/", "hubAndSpoke"},
	"centralized_policies/definitions/control_policy/vpn_membership":         {"vpnmembershipgroup/", "vpnMembershipGroup"},
	"centralized_policies/definitions/data_policy/cflowd":                    {"cflowd/", "cflowd"},
	"security_policies/definitions/intrusion_prevention":                     {"intrusionprevention/", "intrusionPrevention"},
}

func compileLocalizedFeaturePolicies(items []LocalizedFeaturePolicy) ([]Resource, error) {
	out := []Resource{}
	seen := map[string]bool{}
	for _, p := range items {
		if p.Name == "" || len(p.Name) > 128 || p.Description == "" || seen[p.Name] {
			return nil, errors.New("invalid or duplicate localized feature policy")
		}
		seen[p.Name] = true
		settings := map[string]any{}
		for _, field := range []struct {
			key   string
			value *bool
		}{
			{"flowVisibility", p.IPv4FlowVisibility}, {"flowVisibilityIPv6", p.IPv6FlowVisibility},
			{"appVisibility", p.IPv4ApplicationVisibility}, {"appVisibilityIPv6", p.IPv6ApplicationVisibility},
			{"cloudQos", p.CloudQoS}, {"cloudQosServiceSide", p.CloudQoSServiceSide},
			{"implicitAclLogging", p.ImplicitACLLogging},
		} {
			value := false
			if field.value != nil {
				value = *field.value
			}
			settings[field.key] = value
		}
		for _, field := range []struct {
			key      string
			value    *int
			min, max int
		}{
			{"logFrequency", p.LogFrequency, 1, 2147483647},
			{"ipVisibilityCacheEntries", p.IPv4VisibilityCacheEntries, 16, 2000000},
			{"ipV6VisibilityCacheEntries", p.IPv6VisibilityCacheEntries, 16, 2000000},
		} {
			if field.value != nil {
				if *field.value < field.min || *field.value > field.max {
					return nil, fmt.Errorf("invalid %s", field.key)
				}
				settings[field.key] = *field.value
			}
		}
		refs := []ClassicDefinitionReference{}
		assembly := []any{}
		for _, group := range []struct {
			key   string
			names []string
		}{
			{"qos_maps", p.Definitions.QoSMaps}, {"rewrite_rules", p.Definitions.RewriteRules},
			{"route_policies", p.Definitions.RoutePolicies}, {"ipv4_access_control_lists", p.Definitions.IPv4AccessControlLists},
			{"ipv6_access_control_lists", p.Definitions.IPv6AccessControlLists}, {"ipv4_device_access_policies", p.Definitions.IPv4DeviceAccessPolicies},
			{"ipv6_device_access_policies", p.Definitions.IPv6DeviceAccessPolicies},
		} {
			spec := classicDefinitionTypes[group.key]
			for _, name := range group.names {
				if name == "" {
					return nil, errors.New("empty localized policy definition reference")
				}
				refs = append(refs, ClassicDefinitionReference{Type: group.key, Name: name})
				assembly = append(assembly, map[string]any{"type": spec.apiType, "definitionId": ""})
			}
		}
		body := map[string]any{"policyType": "feature", "policyName": p.Name, "policyDescription": p.Description, "policyDefinition": map[string]any{"settings": settings, "assembly": assembly}}
		out = append(out, Resource{Key: "localized_policies/feature_policies/" + p.Name, Name: p.Name, Section: "localized_policies", Kind: "localized-policy", Type: "localized_policy", Collection: "/dataservice/template/policy/vedge/", ClassicDefinitionRefs: refs, Body: body})
	}
	return out, nil
}

func (c *Client) resolveLocalizedDefinitions(ctx context.Context, r Resource) error {
	if r.Kind != "localized-policy" && r.Kind != "centralized-policy" && r.Kind != "security-policy" {
		return nil
	}
	definition, ok := r.Body["policyDefinition"].(map[string]any)
	if !ok {
		return ErrResponse
	}
	assembly, ok := definition["assembly"].([]any)
	if !ok || len(assembly) != len(r.ClassicDefinitionRefs) {
		return ErrResponse
	}
	for i, ref := range r.ClassicDefinitionRefs {
		spec, ok := classicDefinitionTypes[ref.Type]
		if !ok {
			return ErrResponse
		}
		probe := Resource{Name: ref.Name, Kind: "classic-definition", Collection: "/dataservice/template/policy/definition/" + spec.path, Body: map[string]any{"type": spec.apiType}}
		o, e := c.observeClassicDefinition(ctx, probe)
		if e != nil {
			return e
		}
		if o == nil {
			return ErrDependencyPending
		}
		row, ok := assembly[i].(map[string]any)
		if !ok {
			return ErrResponse
		}
		row["definitionId"] = o.ID
	}
	return nil
}

func (c *Client) observeLocalizedPolicy(ctx context.Context, r Resource) (*Observation, error) {
	if e := c.resolveLocalizedDefinitions(ctx, r); e != nil {
		if errors.Is(e, ErrDependencyPending) {
			return nil, nil
		}
		return nil, e
	}
	var list struct {
		Data *[]struct {
			ID   string `json:"policyId"`
			Name string `json:"policyName"`
			Type string `json:"policyType"`
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
			if id != "" || item.Type != "feature" || !validID.MatchString(item.ID) {
				return nil, ErrResponse
			}
			id = item.ID
		}
	}
	if id == "" {
		return nil, nil
	}
	var body map[string]any
	if e := c.Request(ctx, "GET", r.Collection+"definition/"+url.PathEscape(id), nil, &body); e != nil {
		return nil, e
	}
	return &Observation{ID: id, Body: body}, nil
}
func (c *Client) createLocalizedPolicy(ctx context.Context, r Resource) (string, error) {
	if e := c.resolveLocalizedDefinitions(ctx, r); e != nil {
		return "", e
	}
	if r.Kind == "security-policy" {
		// This endpoint returns an empty body after a successful POST. Find the
		// exact fixture identity in the follow-up list, as the pinned provider does.
		if e := c.Request(ctx, "POST", r.Collection, r.Body, nil); e != nil {
			return "", e
		}
		var list struct {
			Data *[]struct {
				ID   string `json:"policyId"`
				Name string `json:"policyName"`
				Type string `json:"policyType"`
			} `json:"data"`
		}
		if e := c.Request(ctx, "GET", r.Collection, nil, &list); e != nil {
			return "", e
		}
		if list.Data == nil {
			return "", ErrResponse
		}
		id := ""
		for _, item := range *list.Data {
			if item.Name == r.Name {
				if id != "" || item.Type != "feature" || !validID.MatchString(item.ID) {
					return "", ErrResponse
				}
				id = item.ID
			}
		}
		if id == "" {
			return "", ErrResponse
		}
		return id, nil
	}
	var result map[string]any
	if e := c.Request(ctx, "POST", r.Collection, r.Body, &result); e != nil {
		return "", e
	}
	id, _ := result["policyId"].(string)
	if !validID.MatchString(id) {
		return "", ErrResponse
	}
	return id, nil
}
func (c *Client) updateLocalizedPolicy(ctx context.Context, r Resource, o *Observation) error {
	if e := c.resolveLocalizedDefinitions(ctx, r); e != nil {
		return e
	}
	return c.Request(ctx, "PUT", r.Collection+url.PathEscape(o.ID), r.Body, nil)
}
func (c *Client) deleteLocalizedPolicy(ctx context.Context, r Resource, id string) error {
	if !validID.MatchString(id) {
		return ErrResponse
	}
	return c.Request(ctx, "DELETE", r.Collection+url.PathEscape(id), nil, nil)
}
func equalLocalizedPolicy(r Resource, observed map[string]any) (bool, error) {
	if observed["policyName"] != r.Name || observed["policyDescription"] != r.Body["policyDescription"] || !strings.EqualFold(fmt.Sprint(observed["policyType"]), "feature") {
		return false, nil
	}
	actual, ok := observed["policyDefinition"].(map[string]any)
	if !ok {
		return false, ErrResponse
	}
	wanted := r.Body["policyDefinition"].(map[string]any)
	if r.Kind == "centralized-policy" {
		return equalCentralizedAssembly(actual, wanted)
	}
	if r.Kind == "security-policy" {
		if observed["policyMode"] != r.Body["policyMode"] || observed["policyUseCase"] != r.Body["policyUseCase"] {
			return false, nil
		}
		return equalCentralizedAssembly(actual, wanted)
	}
	actualSettings, ok := actual["settings"].(map[string]any)
	if !ok {
		return false, ErrResponse
	}
	for key, value := range wanted["settings"].(map[string]any) {
		if !reflect.DeepEqual(actualSettings[key], value) {
			return false, nil
		}
	}
	actualAssembly, ok := actual["assembly"].([]any)
	if !ok {
		return false, ErrResponse
	}
	wantAssembly := wanted["assembly"].([]any)
	if len(actualAssembly) != len(wantAssembly) {
		return false, nil
	}
	for i, entry := range actualAssembly {
		row, ok := entry.(map[string]any)
		if !ok {
			return false, ErrResponse
		}
		want := wantAssembly[i].(map[string]any)
		if !strings.EqualFold(fmt.Sprint(row["type"]), fmt.Sprint(want["type"])) || row["definitionId"] != want["definitionId"] {
			return false, nil
		}
	}
	return true, nil
}

func equalCentralizedAssembly(actual, wanted map[string]any) (bool, error) {
	rows, ok := actual["assembly"].([]any)
	if !ok {
		return false, ErrResponse
	}
	expected := wanted["assembly"].([]any)
	if len(rows) != len(expected) {
		return false, nil
	}
	for i, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			return false, ErrResponse
		}
		want := expected[i].(map[string]any)
		if !strings.EqualFold(fmt.Sprint(row["type"]), fmt.Sprint(want["type"])) || row["definitionId"] != want["definitionId"] {
			return false, nil
		}
	}
	return true, nil
}
