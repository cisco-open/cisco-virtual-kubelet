// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

//go:embed definitions/route-policy-schemas.json
var routePolicySchemas []byte

func compileRoutePolicies(p FeatureProfile, family, key string) ([]Resource, error) {
	if len(p.RoutePolicies) == 0 {
		return nil, nil
	}
	if family != "service" && family != "transport" {
		return nil, errors.New("route policies require a service or transport profile")
	}
	out := []Resource{}
	seen := map[string]bool{}
	for _, input := range p.RoutePolicies {
		name, body, refs, err := routePolicyData(input, family)
		if err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, errors.New("duplicate route policy name")
		}
		seen[name] = true
		out = append(out, Resource{Key: key + "/route_policies/" + name, Name: name, Section: "feature_profiles", Kind: "parcel", Type: family + "_route_policy", Collection: "/dataservice/v1/feature-profile/sdwan/" + family, ParentName: p.Name, ParcelPath: "route-policy", ParcelRefs: refs, Body: body})
	}
	return out, nil
}

func routePolicyData(input map[string]any, family string) (string, map[string]any, []ParcelReference, error) {
	for field := range input {
		switch field {
		case "name", "description", "default_action", "sequences":
		default:
			return "", nil, nil, fmt.Errorf("unsupported route policy field %s", field)
		}
	}
	name, ok := input["name"].(string)
	if !ok || name == "" || len(name) > 128 {
		return "", nil, nil, errors.New("invalid route policy name")
	}
	description := ""
	if input["description"] != nil {
		var ok bool
		description, ok = input["description"].(string)
		if !ok {
			return "", nil, nil, errors.New("invalid route policy description")
		}
	}
	defaultAction := map[string]any{"optionType": "default", "value": "reject"}
	if value := input["default_action"]; value != nil {
		defaultAction = aclOption(value)
	}
	data := map[string]any{"defaultAction": defaultAction, "sequences": []any{}}
	refs := []ParcelReference{}
	if raw := input["sequences"]; raw != nil {
		sequences, ok := raw.([]any)
		if !ok {
			return "", nil, nil, errors.New("route policy sequences must be an array")
		}
		out := []any{}
		for i, item := range sequences {
			seq, ok := item.(map[string]any)
			if !ok {
				return "", nil, nil, errors.New("route policy sequence must be an object")
			}
			for field := range seq {
				switch field {
				case "id", "name", "base_action", "protocol", "actions", "match_entries":
				default:
					return "", nil, nil, fmt.Errorf("unsupported route policy sequence field %s", field)
				}
			}
			id, ok := seq["id"].(float64)
			if !ok {
				return "", nil, nil, errors.New("route policy sequence id is required")
			}
			seqName := "route"
			if seq["name"] != nil {
				var ok bool
				seqName, ok = seq["name"].(string)
				if !ok {
					return "", nil, nil, errors.New("invalid route policy sequence name")
				}
			}
			base, ok := seq["base_action"].(string)
			if !ok {
				return "", nil, nil, errors.New("route policy base_action is required")
			}
			protocol := "IPV4"
			if seq["protocol"] != nil {
				var ok bool
				protocol, ok = seq["protocol"].(string)
				if !ok {
					return "", nil, nil, errors.New("invalid route policy protocol")
				}
				protocol = strings.ToUpper(protocol)
			}
			entry := map[string]any{"sequenceId": aclOption(id), "sequenceName": aclOption(seqName), "baseAction": aclOption(base), "protocol": aclOption(protocol)}
			if seq["actions"] != nil {
				actions, ok := seq["actions"].(map[string]any)
				if !ok {
					return "", nil, nil, errors.New("route policy actions must be an object")
				}
				if len(actions) > 0 {
					mapped, err := routePolicyActions(actions)
					if err != nil {
						return "", nil, nil, err
					}
					entry["actions"] = []any{mapped}
				}
			}
			if seq["match_entries"] != nil {
				matches, ok := seq["match_entries"].(map[string]any)
				if !ok {
					return "", nil, nil, errors.New("route policy match_entries must be an object")
				}
				if len(matches) > 0 {
					mapped, more, err := routePolicyMatch(matches, i)
					if err != nil {
						return "", nil, nil, err
					}
					entry["matchEntries"] = []any{mapped}
					refs = append(refs, more...)
				}
			}
			out = append(out, entry)
		}
		data["sequences"] = out
	}
	var schemas map[string]map[string]any
	encoded, _ := json.Marshal(data)
	var normalized any
	if json.Unmarshal(routePolicySchemas, &schemas) != nil || json.Unmarshal(encoded, &normalized) != nil || !validScalarValue(normalized, schemas[family]) {
		return "", nil, nil, errors.New("route policy intent violates provider schema")
	}
	sort.Slice(refs, func(i, j int) bool { return strings.Join(refs[i].BodyPath, "/") < strings.Join(refs[j].BodyPath, "/") })
	return name, map[string]any{"name": name, "description": description, "data": data}, refs, nil
}

func routePolicyActions(input map[string]any) (map[string]any, error) {
	out := map[string]any{"accept": map[string]any{"enableAcceptAction": map[string]any{"optionType": "default", "value": true}, "setCommunity": map[string]any{"additive": map[string]any{"optionType": "default", "value": false}}}}
	for field, value := range input {
		switch field {
		case "prepend_as_paths":
			setScalarPath(out, "accept.setAsPath.prepend", aclOption(value))
		case "communities":
			if input["communities_variable"] != nil {
				return nil, errors.New("communities and variable conflict")
			}
			setScalarPath(out, "accept.setCommunity.community", aclOption(value))
		case "communities_variable":
			if input["communities"] != nil {
				return nil, errors.New("communities and variable conflict")
			}
			opt, err := aclVariable(value)
			if err != nil {
				return nil, err
			}
			setScalarPath(out, "accept.setCommunity.community", opt)
		case "communities_additive":
			setScalarPath(out, "accept.setCommunity.additive", aclOption(value))
		case "ipv4_next_hop":
			setScalarPath(out, "accept.setIpv4NextHop", aclOption(value))
		case "ipv6_next_hop":
			setScalarPath(out, "accept.setIpv6NextHop", aclOption(value))
		case "bgp_local_preference":
			setScalarPath(out, "accept.setLocalPreference", aclOption(value))
		case "metric":
			setScalarPath(out, "accept.setMetric", aclOption(value))
		case "metric_type":
			setScalarPath(out, "accept.setMetricType", aclOption(value))
		case "omp_tag":
			setScalarPath(out, "accept.setOmpTag", aclOption(value))
		case "origin":
			origin, ok := value.(string)
			if !ok {
				return nil, errors.New("invalid route policy origin")
			}
			switch origin {
			case "igp":
				origin = "IGP"
			case "egp":
				origin = "EGP"
			case "incomplete":
				origin = "Incomplete"
			default:
				return nil, errors.New("invalid route policy origin")
			}
			setScalarPath(out, "accept.setOrigin", aclOption(origin))
		case "ospf_tag":
			setScalarPath(out, "accept.setOspfTag", aclOption(value))
		case "weight":
			setScalarPath(out, "accept.setWeight", aclOption(value))
		default:
			return nil, fmt.Errorf("unsupported route policy action %s", field)
		}
	}
	return out, nil
}

func routePolicyMatch(input map[string]any, index int) (map[string]any, []ParcelReference, error) {
	out := map[string]any{}
	refs := []ParcelReference{}
	refSpecs := map[string]struct{ kind, path, api string }{
		"as_path_list":              {"policy_object_as_path_list", "as-path", "asPathList"},
		"expanded_community_list":   {"policy_object_expanded_community_list", "expanded-community", "communityList.expandedCommunityList"},
		"extended_community_list":   {"policy_object_extended_community_list", "ext-community", "extCommunityList"},
		"ipv4_address_prefix_list":  {"policy_object_ipv4_prefix_list", "prefix", "ipv4Address"},
		"ipv4_next_hop_prefix_list": {"policy_object_ipv4_prefix_list", "prefix", "ipv4NextHop"},
		"ipv6_address_prefix_list":  {"policy_object_ipv6_prefix_list", "ipv6-prefix", "ipv6Address"},
		"ipv6_next_hop_prefix_list": {"policy_object_ipv6_prefix_list", "ipv6-prefix", "ipv6NextHop"},
	}
	for field, value := range input {
		if spec, ok := refSpecs[field]; ok {
			bodyPath := append([]string{"data", "sequences", fmt.Sprint(index), "matchEntries", "0"}, strings.Split(spec.api, ".")...)
			bodyPath = append(bodyPath, "refId", "value")
			opt, ref, err := aclReference(value, spec.kind, spec.path, bodyPath)
			if err != nil {
				return nil, nil, err
			}
			setScalarPath(out, spec.api+".refId", opt)
			refs = append(refs, ref)
			continue
		}
		switch field {
		case "bgp_local_preference":
			out["bgpLocalPreference"] = aclOption(value)
		case "metric":
			out["metric"] = aclOption(value)
		case "omp_tag":
			out["ompTag"] = aclOption(value)
		case "ospf_tag":
			out["ospfTag"] = aclOption(value)
		case "standard_community_lists_criteria":
			criterion, ok := value.(string)
			if !ok {
				return nil, nil, errors.New("invalid community criteria")
			}
			setScalarPath(out, "communityList.criteria", aclOption(strings.ToUpper(criterion)))
		case "standard_community_lists":
			items, ok := value.([]any)
			if !ok {
				return nil, nil, errors.New("standard community lists must be an array")
			}
			rows := []any{}
			for j, item := range items {
				path := []string{"data", "sequences", fmt.Sprint(index), "matchEntries", "0", "communityList", "standardCommunityList", fmt.Sprint(j), "refId", "value"}
				opt, ref, err := aclReference(item, "policy_object_standard_community_list", "standard-community", path)
				if err != nil {
					return nil, nil, err
				}
				rows = append(rows, map[string]any{"refId": opt})
				refs = append(refs, ref)
			}
			setScalarPath(out, "communityList.standardCommunityList", rows)
		default:
			return nil, nil, fmt.Errorf("unsupported route policy match field %s", field)
		}
	}
	return out, refs, nil
}
