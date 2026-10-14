// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"reflect"
	"strings"
)

type LocalizedPolicies struct {
	Definitions     LocalizedPolicyDefinitions `json:"definitions"`
	FeaturePolicies []LocalizedFeaturePolicy   `json:"feature_policies,omitempty"`
}
type LocalizedPolicyDefinitions struct {
	QoSMaps                  []ClassicQoSMap      `json:"qos_maps,omitempty"`
	RewriteRules             []ClassicRewriteRule `json:"rewrite_rules,omitempty"`
	IPv4AccessControlLists   []ClassicACL         `json:"ipv4_access_control_lists,omitempty"`
	IPv6AccessControlLists   []ClassicACL         `json:"ipv6_access_control_lists,omitempty"`
	IPv4DeviceAccessPolicies []ClassicACL         `json:"ipv4_device_access_policies,omitempty"`
	IPv6DeviceAccessPolicies []ClassicACL         `json:"ipv6_device_access_policies,omitempty"`
	RoutePolicies            []ClassicRoutePolicy `json:"route_policies,omitempty"`
}
type ClassicQoSMap struct {
	Name          string                `json:"name"`
	Description   string                `json:"description"`
	QoSSchedulers []ClassicQoSScheduler `json:"qos_schedulers"`
}
type ClassicQoSScheduler struct {
	Queue            int    `json:"queue"`
	ClassMap         string `json:"class_map,omitempty"`
	BandwidthPercent int    `json:"bandwidth_percent"`
	BufferPercent    int    `json:"buffer_percent"`
	BurstBytes       *int   `json:"burst_bytes,omitempty"`
	DropType         string `json:"drop_type"`
	SchedulingType   string `json:"scheduling_type"`
}
type ClassicRewriteRule struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Rules       []ClassicRewriteEntry `json:"rules"`
}
type ClassicRewriteEntry struct {
	Class     string `json:"class"`
	Priority  string `json:"priority"`
	DSCP      int    `json:"dscp"`
	Layer2COS *int   `json:"layer2_cos,omitempty"`
}
type ClassicRoutePolicy struct {
	Name          string                 `json:"name"`
	Description   string                 `json:"description"`
	DefaultAction string                 `json:"default_action"`
	Sequences     []ClassicRouteSequence `json:"sequences"`
}
type ClassicRouteSequence struct {
	ID            int    `json:"id"`
	IPType        string `json:"ip_type,omitempty"`
	Name          string `json:"name,omitempty"`
	BaseAction    string `json:"base_action"`
	MatchCriteria struct {
		OMPTag *int `json:"omp_tag,omitempty"`
	} `json:"match_criterias"`
	Actions struct {
		Metric *int `json:"metric,omitempty"`
	} `json:"actions"`
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
	DestinationPort     *int   `json:"destination_port,omitempty"`
}

func compileLocalizedPolicies(input *LocalizedPolicies) ([]Resource, error) {
	if input == nil {
		return nil, nil
	}
	out := []Resource{}
	for _, qos := range input.Definitions.QoSMaps {
		if qos.Name == "" || len(qos.Name) > 128 || qos.Description == "" {
			return nil, errors.New("invalid localized QoS map")
		}
		rows := []any{}
		refs := map[string]string{}
		queues := map[int]bool{}
		for i, s := range qos.QoSSchedulers {
			if s.Queue < 0 || s.Queue > 7 || queues[s.Queue] || (s.Queue != 0 && s.ClassMap == "") || s.BandwidthPercent < 0 || s.BandwidthPercent > 100 || s.BufferPercent < 0 || s.BufferPercent > 100 || s.DropType == "" || s.SchedulingType == "" {
				return nil, errors.New("invalid QoS scheduler")
			}
			queues[s.Queue] = true
			row := map[string]any{"queue": fmt.Sprint(s.Queue), "classMapRef": "", "bandwidthPercent": fmt.Sprint(s.BandwidthPercent), "bufferPercent": fmt.Sprint(s.BufferPercent), "drops": s.DropType, "scheduling": s.SchedulingType}
			if s.BurstBytes != nil {
				if *s.BurstBytes < 0 {
					return nil, errors.New("invalid QoS burst")
				}
				row["burst"] = fmt.Sprint(*s.BurstBytes)
			}
			if s.ClassMap != "" {
				refs[fmt.Sprint(i)] = s.ClassMap
			}
			rows = append(rows, row)
		}
		out = append(out, Resource{Key: "localized_policies/definitions/qos_maps/" + qos.Name, Name: qos.Name, Section: "localized_policies", Kind: "classic-definition", Type: "qos_map_policy_definition", Collection: "/dataservice/template/policy/definition/qosmap/", ProfileRefs: refs, Body: map[string]any{"type": "qosMap", "name": qos.Name, "description": qos.Description, "definition": map[string]any{"qosSchedulers": rows}}})
	}
	for _, rewrite := range input.Definitions.RewriteRules {
		if rewrite.Name == "" || len(rewrite.Name) > 128 || rewrite.Description == "" {
			return nil, errors.New("invalid localized rewrite rule")
		}
		rows := []any{}
		refs := map[string]string{}
		for i, rule := range rewrite.Rules {
			if rule.Class == "" || rule.Priority == "" || rule.DSCP < 0 || rule.DSCP > 63 {
				return nil, errors.New("invalid localized rewrite entry")
			}
			row := map[string]any{"class": "", "plp": rule.Priority, "dscp": fmt.Sprint(rule.DSCP)}
			if rule.Layer2COS != nil {
				if *rule.Layer2COS < 0 || *rule.Layer2COS > 7 {
					return nil, errors.New("invalid layer2_cos")
				}
				row["layer2Cos"] = fmt.Sprint(*rule.Layer2COS)
			}
			refs[fmt.Sprint(i)] = rule.Class
			rows = append(rows, row)
		}
		out = append(out, Resource{Key: "localized_policies/definitions/rewrite_rules/" + rewrite.Name, Name: rewrite.Name, Section: "localized_policies", Kind: "classic-definition", Type: "rewrite_rule_policy_definition", Collection: "/dataservice/template/policy/definition/rewriterule/", ProfileRefs: refs, Body: map[string]any{"type": "rewriteRule", "name": rewrite.Name, "description": rewrite.Description, "definition": map[string]any{"rules": rows}}})
	}
	for _, spec := range []struct {
		family, apiType, sequenceType, path, modelType, keyPart, sourceField, destinationField, defaultName string
		items                                                                                               []ClassicACL
	}{
		{"ipv4", "acl", "acl", "acl/", "ipv4_acl_policy_definition", "ipv4_access_control_lists", "sourceIp", "destinationIp", "Access Control List", input.Definitions.IPv4AccessControlLists},
		{"ipv6", "aclv6", "aclv6", "aclv6/", "ipv6_acl_policy_definition", "ipv6_access_control_lists", "sourceIpv6", "destinationIpv6", "Access Control List", input.Definitions.IPv6AccessControlLists},
		{"ipv4", "deviceAccessPolicy", "deviceaccesspolicy", "deviceaccesspolicy/", "ipv4_device_acl_policy_definition", "ipv4_device_access_policies", "sourceIp", "destinationIp", "Device Access Control List", input.Definitions.IPv4DeviceAccessPolicies},
		{"ipv6", "deviceaccesspolicyv6", "deviceaccesspolicyv6", "deviceaccesspolicyv6/", "ipv6_device_acl_policy_definition", "ipv6_device_access_policies", "sourceIpv6", "destinationIpv6", "Device Access Control List", input.Definitions.IPv6DeviceAccessPolicies},
	} {
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
					name = spec.defaultName
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
				if s.MatchCriteria.DestinationPort != nil {
					port := *s.MatchCriteria.DestinationPort
					if !strings.Contains(spec.modelType, "device_acl") || (port != 22 && port != 161) {
						return nil, errors.New("invalid device ACL destination_port")
					}
					matches = append(matches, map[string]any{"field": "destinationPort", "value": fmt.Sprint(port)})
				}
				if strings.Contains(spec.modelType, "device_acl") && s.MatchCriteria.DestinationPort == nil {
					return nil, errors.New("device ACL requires destination_port 22 or 161")
				}
				sequence := map[string]any{"sequenceId": s.ID, "sequenceName": name, "sequenceType": spec.sequenceType, "baseAction": s.BaseAction, "match": map[string]any{"entries": matches}, "actions": []any{}}
				if spec.family == "ipv6" {
					sequence["sequenceIpType"] = "ipv6"
				}
				sequences = append(sequences, sequence)
			}
			body := map[string]any{"type": spec.apiType, "name": acl.Name, "description": acl.Description, "defaultAction": map[string]any{"type": acl.DefaultAction}, "sequences": sequences}
			out = append(out, Resource{Key: "localized_policies/definitions/" + spec.keyPart + "/" + acl.Name, Name: acl.Name, Section: "localized_policies", Kind: "classic-definition", Type: spec.modelType, Collection: "/dataservice/template/policy/definition/" + spec.path, Body: body})
		}
	}
	for _, route := range input.Definitions.RoutePolicies {
		if route.Name == "" || route.Description == "" || len(route.Name) > 128 || (route.DefaultAction != "accept" && route.DefaultAction != "reject") {
			return nil, errors.New("invalid localized route policy")
		}
		sequences := []any{}
		seen := map[int]bool{}
		for _, sequence := range route.Sequences {
			if sequence.ID < 1 || sequence.ID > 65535 || seen[sequence.ID] || (sequence.BaseAction != "accept" && sequence.BaseAction != "reject") {
				return nil, errors.New("invalid localized route sequence")
			}
			seen[sequence.ID] = true
			ipType := sequence.IPType
			if ipType == "" {
				ipType = "ipv4"
			}
			if ipType != "ipv4" && ipType != "ipv6" {
				return nil, errors.New("invalid localized route ip_type")
			}
			name := sequence.Name
			if name == "" {
				name = "route"
			}
			matches := []any{}
			if sequence.MatchCriteria.OMPTag != nil {
				if *sequence.MatchCriteria.OMPTag < 0 || *sequence.MatchCriteria.OMPTag > 4294967295 {
					return nil, errors.New("invalid OMP tag")
				}
				matches = append(matches, map[string]any{"field": "ompTag", "value": fmt.Sprint(*sequence.MatchCriteria.OMPTag)})
			}
			actions := []any{}
			if sequence.Actions.Metric != nil {
				if *sequence.Actions.Metric < 0 {
					return nil, errors.New("invalid route metric")
				}
				actions = append(actions, map[string]any{"type": "set", "parameter": []any{map[string]any{"field": "metric", "value": fmt.Sprint(*sequence.Actions.Metric)}}})
			}
			sequences = append(sequences, map[string]any{"sequenceId": sequence.ID, "sequenceIpType": ipType, "sequenceName": name, "sequenceType": "vedgeRoute", "baseAction": sequence.BaseAction, "match": map[string]any{"entries": matches}, "actions": actions})
		}
		body := map[string]any{"type": "vedgeRoute", "name": route.Name, "description": route.Description, "defaultAction": map[string]any{"type": route.DefaultAction}, "sequences": sequences}
		out = append(out, Resource{Key: "localized_policies/definitions/route_policies/" + route.Name, Name: route.Name, Section: "localized_policies", Kind: "classic-definition", Type: "route_policy_definition", Collection: "/dataservice/template/policy/definition/vedgeroute/", Body: body})
	}
	featurePolicies, err := compileLocalizedFeaturePolicies(input.FeaturePolicies)
	if err != nil {
		return nil, err
	}
	out = append(out, featurePolicies...)
	return out, nil
}
func (c *Client) observeClassicDefinition(ctx context.Context, r Resource) (*Observation, error) {
	if e := c.resolveClassicPolicyLists(ctx, r); e != nil {
		if errors.Is(e, ErrDependencyPending) {
			return nil, nil
		}
		return nil, e
	}
	if e := c.resolveClassicClassMaps(ctx, r); e != nil {
		if errors.Is(e, ErrDependencyPending) {
			return nil, nil
		}
		return nil, e
	}
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
			if id != "" || !strings.EqualFold(item.Type, r.Body["type"].(string)) || !validID.MatchString(item.ID) {
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
	if e := c.resolveClassicPolicyLists(ctx, r); e != nil {
		return "", e
	}
	if e := c.resolveClassicClassMaps(ctx, r); e != nil {
		return "", e
	}
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
	if e := c.resolveClassicPolicyLists(ctx, r); e != nil {
		return e
	}
	if e := c.resolveClassicClassMaps(ctx, r); e != nil {
		return e
	}
	return c.Request(ctx, "PUT", r.Collection+url.PathEscape(o.ID), r.Body, nil)
}
func (c *Client) deleteClassicDefinition(ctx context.Context, r Resource, id string) error {
	if !validID.MatchString(id) {
		return ErrResponse
	}
	return c.Request(ctx, "DELETE", r.Collection+url.PathEscape(id), nil, nil)
}
func (c *Client) resolveClassicClassMaps(ctx context.Context, r Resource) error {
	if len(r.ProfileRefs) == 0 {
		return nil
	}
	definition, ok := r.Body["definition"].(map[string]any)
	if !ok {
		return ErrResponse
	}
	field, target := "qosSchedulers", "classMapRef"
	if r.Type == "rewrite_rule_policy_definition" {
		field, target = "rules", "class"
	}
	rows, ok := definition[field].([]any)
	if !ok {
		return ErrResponse
	}
	for index, name := range r.ProfileRefs {
		i := -1
		if _, e := fmt.Sscan(index, &i); e != nil || i < 0 || i >= len(rows) {
			return ErrResponse
		}
		probe := Resource{Name: name, Type: "class", Kind: "policy-list", Collection: "/dataservice/template/policy/list/class"}
		o, e := c.observePolicyList(ctx, probe)
		if e != nil {
			return e
		}
		if o == nil {
			return fmt.Errorf("class map %q: %w", name, ErrDependencyPending)
		}
		row, ok := rows[i].(map[string]any)
		if !ok {
			return ErrResponse
		}
		row[target] = o.ID
	}
	return nil
}
func equalClassicDefinition(r Resource, o map[string]any) (bool, error) {
	if o["name"] != r.Name || !strings.EqualFold(fmt.Sprint(o["type"]), r.Body["type"].(string)) {
		return false, nil
	}
	if description, _ := o["description"].(string); description != r.Body["description"] {
		return false, nil
	}
	if r.Type == "qos_map_policy_definition" || r.Type == "rewrite_rule_policy_definition" || r.Type == "vpn_membership_policy_definition" || r.Type == "mesh_topology_policy_definition" || r.Type == "hub_and_spoke_topology_policy_definition" || r.Type == "cflowd_policy_definition" || r.Type == "intrusion_prevention_policy_definition" || r.Type == "zone_based_firewall_policy_definition" {
		if (r.Type == "intrusion_prevention_policy_definition" || r.Type == "zone_based_firewall_policy_definition") && o["mode"] != r.Body["mode"] {
			return false, nil
		}
		actual, ok := o["definition"].(map[string]any)
		if !ok {
			return false, ErrResponse
		}
		want := r.Body["definition"].(map[string]any)
		encoded, err := json.Marshal(want)
		if err != nil {
			return false, ErrResponse
		}
		var normalized map[string]any
		if json.Unmarshal(encoded, &normalized) != nil {
			return false, ErrResponse
		}
		for key, expected := range normalized {
			if !reflect.DeepEqual(actual[key], expected) {
				return false, nil
			}
		}
		return true, nil
	}
	actual := map[string]any{"type": r.Body["type"], "name": o["name"], "description": o["description"], "defaultAction": o["defaultAction"], "sequences": o["sequences"]}
	if actual["sequences"] == nil {
		actual["sequences"] = []any{}
	}
	if action, ok := actual["defaultAction"].(map[string]any); ok {
		actual["defaultAction"] = map[string]any{"type": action["type"]}
	}
	if rows, ok := actual["sequences"].([]any); ok {
		normalized := []any{}
		for i, row := range rows {
			entry, ok := row.(map[string]any)
			if !ok {
				return false, ErrResponse
			}
			sequenceType := entry["sequenceType"]
			if i < len(r.Body["sequences"].([]any)) {
				wantType := r.Body["sequences"].([]any)[i].(map[string]any)["sequenceType"].(string)
				if strings.EqualFold(fmt.Sprint(sequenceType), wantType) {
					sequenceType = wantType
				}
			}
			sequence := map[string]any{"sequenceId": entry["sequenceId"], "sequenceName": entry["sequenceName"], "sequenceType": sequenceType, "baseAction": entry["baseAction"], "match": entry["match"], "actions": entry["actions"]}
			if r.Type == "ipv6_acl_policy_definition" || r.Type == "ipv6_device_acl_policy_definition" || r.Type == "route_policy_definition" {
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
