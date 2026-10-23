// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

//go:embed definitions/ipv6-acl-schemas.json
var ipv6ACLSchemas []byte

func compileIPv6ACLs(p FeatureProfile, family, key string) ([]Resource, error) {
	if len(p.IPv6ACLs) == 0 {
		return nil, nil
	}
	if family != "service" && family != "transport" {
		return nil, errors.New("IPv6 ACLs require a service or transport profile")
	}
	out := []Resource{}
	seen := map[string]bool{}
	for _, input := range p.IPv6ACLs {
		name, body, refs, err := ipv6ACLData(input, family)
		if err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, errors.New("duplicate IPv6 ACL name")
		}
		seen[name] = true
		out = append(out, Resource{Key: key + "/ipv6_acls/" + name, Name: name, Section: "feature_profiles", Kind: "parcel", Type: family + "_ipv6_acl", Collection: "/dataservice/v1/feature-profile/sdwan/" + family, ParentName: p.Name, ParcelPath: "ipv6-acl", ParcelRefs: refs, Body: body})
	}
	return out, nil
}

func ipv6ACLData(input map[string]any, family string) (string, map[string]any, []ParcelReference, error) {
	for field := range input {
		switch field {
		case "name", "description", "default_action", "sequences":
		default:
			return "", nil, nil, fmt.Errorf("unsupported IPv6 ACL field %s", field)
		}
	}
	name, ok := input["name"].(string)
	if !ok || name == "" || len(name) > 128 {
		return "", nil, nil, errors.New("invalid IPv6 ACL name")
	}
	description := ""
	if input["description"] != nil {
		var ok bool
		description, ok = input["description"].(string)
		if !ok {
			return "", nil, nil, errors.New("invalid IPv6 ACL description")
		}
	}
	defaultAction, hasDefault := input["default_action"].(string)
	if !hasDefault {
		if family == "transport" {
			return "", nil, nil, errors.New("transport IPv6 ACL default_action is required")
		}
		defaultAction = "drop"
	}
	if defaultAction != "drop" && defaultAction != "accept" {
		return "", nil, nil, errors.New("invalid IPv6 ACL default_action")
	}
	defaultOption := map[string]any{"optionType": "global", "value": defaultAction}
	data := map[string]any{"defaultAction": defaultOption, "sequences": []any{}}
	refs := []ParcelReference{}
	if raw := input["sequences"]; raw != nil {
		sequences, ok := raw.([]any)
		if !ok {
			return "", nil, nil, errors.New("IPv6 ACL sequences must be an array")
		}
		out := make([]any, 0, len(sequences))
		for i, item := range sequences {
			seq, ok := item.(map[string]any)
			if !ok {
				return "", nil, nil, errors.New("IPv6 ACL sequence must be an object")
			}
			for field := range seq {
				switch field {
				case "id", "name", "base_action", "actions", "match_entries":
				default:
					return "", nil, nil, fmt.Errorf("unsupported IPv6 ACL sequence field %s", field)
				}
			}
			id, ok := seq["id"].(float64)
			if !ok {
				return "", nil, nil, errors.New("IPv6 ACL sequence id is required")
			}
			seqName := "acl"
			if seq["name"] != nil {
				var ok bool
				seqName, ok = seq["name"].(string)
				if !ok {
					return "", nil, nil, errors.New("invalid IPv6 ACL sequence name")
				}
			}
			base, ok := seq["base_action"].(string)
			if !ok || (base != "accept" && base != "drop") {
				return "", nil, nil, errors.New("invalid IPv6 ACL base_action")
			}
			entry := map[string]any{"sequenceId": ipv6AclOption(id), "sequenceName": ipv6AclOption(seqName)}
			actions, ok := seq["actions"].(map[string]any)
			if seq["actions"] != nil && !ok {
				return "", nil, nil, errors.New("IPv6 ACL actions must be an object")
			}
			if len(actions) == 0 {
				entry["baseAction"] = ipv6AclOption(base)
			} else {
				act, more, err := ipv6ACLActions(actions, base, family, i)
				if err != nil {
					return "", nil, nil, err
				}
				entry["actions"] = []any{act}
				refs = append(refs, more...)
			}
			matches, ok := seq["match_entries"].(map[string]any)
			if seq["match_entries"] != nil && !ok {
				return "", nil, nil, errors.New("IPv6 ACL match_entries must be an object")
			}
			if len(matches) > 0 {
				match, more, err := ipv6ACLMatch(matches, i)
				if err != nil {
					return "", nil, nil, err
				}
				entry["matchEntries"] = []any{match}
				refs = append(refs, more...)
			}
			out = append(out, entry)
		}
		data["sequences"] = out
	}
	var schemas map[string]map[string]any
	encoded, _ := json.Marshal(data)
	var normalized any
	if json.Unmarshal(ipv6ACLSchemas, &schemas) != nil || json.Unmarshal(encoded, &normalized) != nil || !validScalarValue(normalized, schemas[family]) {
		return "", nil, nil, errors.New("IPv6 ACL intent violates provider schema")
	}
	return name, map[string]any{"name": name, "description": description, "data": data}, refs, nil
}

func ipv6AclOption(value any) map[string]any {
	return map[string]any{"optionType": "global", "value": value}
}
func ipv6AclVariable(value any) (map[string]any, error) {
	name, ok := value.(string)
	if !ok || !validParcelVariable.MatchString(name) {
		return nil, errors.New("invalid ACL variable")
	}
	return map[string]any{"optionType": "variable", "value": "{{" + name + "}}"}, nil
}
func ipv6AclReference(name any, kind, path string, bodyPath []string) (map[string]any, ParcelReference, error) {
	text, ok := name.(string)
	if !ok || text == "" || len(text) > 128 {
		return nil, ParcelReference{}, errors.New("invalid ACL policy-object reference")
	}
	ref := ParcelReference{Kind: kind, Name: text, ParcelPath: path, Collection: "/dataservice/v1/feature-profile/sdwan/policy-object", ParentName: "$policy_objects", BodyPath: bodyPath}
	return ipv6AclOption(parcelPlaceholder(text)), ref, nil
}

func ipv6ACLMatch(input map[string]any, index int) (map[string]any, []ParcelReference, error) {
	out := map[string]any{}
	refs := []ParcelReference{}
	for _, direction := range []string{"source", "destination"} {
		count := 0
		for _, suffix := range []string{"data_prefix", "data_prefix_list"} {
			if input[direction+"_"+suffix] != nil {
				count++
			}
		}
		if count > 1 {
			return nil, nil, errors.New("conflicting IPv6 ACL prefix inputs")
		}
	}
	for field, value := range input {
		switch field {
		case "source_data_prefix", "destination_data_prefix", "source_data_prefix_list", "destination_data_prefix_list":
			direction := "source"
			if strings.HasPrefix(field, "destination_") {
				direction = "destination"
			}
			api := direction + "DataPrefix"
			if strings.HasSuffix(field, "_list") {
				path := []string{"data", "sequences", fmt.Sprint(index), "matchEntries", "0", api, api + "List", "refId", "value"}
				opt, ref, err := ipv6AclReference(value, "policy_object_data_ipv6_prefix_list", "data-ipv6-prefix", path)
				if err != nil {
					return nil, nil, err
				}
				setScalarPath(out, api+"."+api+"List.refId", opt)
				refs = append(refs, ref)
			} else {
				setScalarPath(out, api+"."+direction+"IpPrefix", ipv6AclOption(value))
			}
		case "traffic_classes":
			out["trafficClass"] = ipv6AclOption(value)
		case "icmpv6_messages":
			out["icmp6Msg"] = ipv6AclOption(value)
		case "packet_length":
			out["packetLength"] = ipv6AclOption(value)
		case "next_header":
			out["nextHeader"] = ipv6AclOption(value)
		case "tcp_state":
			out["tcp"] = ipv6AclOption(value)
		case "source_ports", "destination_ports":
			ports, ok := value.([]any)
			if !ok {
				return nil, nil, errors.New("ACL ports must be an array")
			}
			rows := []any{}
			api := "sourcePort"
			if field == "destination_ports" {
				api = "destinationPort"
			}
			for _, port := range ports {
				rows = append(rows, map[string]any{api: ipv6AclOption(port)})
			}
			out[ipv6FieldToAPI(field)] = rows
		default:
			return nil, nil, fmt.Errorf("unsupported IPv6 ACL match field %s", field)
		}
	}
	return out, refs, nil
}
func ipv6FieldToAPI(field string) string {
	if field == "source_ports" {
		return "sourcePorts"
	}
	return "destinationPorts"
}

func ipv6ACLActions(input map[string]any, base, family string, index int) (map[string]any, []ParcelReference, error) {
	out := map[string]any{}
	refs := []ParcelReference{}
	chainName := input["service_chain_name"] != nil || input["service_chain_name_variable"] != nil
	for field, value := range input {
		if base == "drop" && field != "counter_name" && field != "log" {
			return nil, nil, errors.New("accept action supplied to drop sequence")
		}
		api := "accept"
		if base == "drop" {
			api = "drop"
		}
		switch field {
		case "counter_name":
			setScalarPath(out, api+".counterName", ipv6AclOption(value))
		case "log":
			setScalarPath(out, api+".log", ipv6AclOption(value))
		case "mirror", "policer":
			kind := "policy_object_" + field
			path := field
			bodyPath := []string{"data", "sequences", fmt.Sprint(index), "actions", "0", "accept", field, "refId", "value"}
			opt, ref, err := ipv6AclReference(value, kind, path, bodyPath)
			if err != nil {
				return nil, nil, err
			}
			setScalarPath(out, "accept."+field+".refId", opt)
			refs = append(refs, ref)
		case "traffic_class":
			setScalarPath(out, "accept.setTrafficClass", ipv6AclOption(value))
		case "ipv6_next_hop":
			setScalarPath(out, "accept.setNextHop", ipv6AclOption(value))
		case "service_chain_name", "service_chain_name_variable":
			if input["service_chain_name"] != nil && input["service_chain_name_variable"] != nil {
				return nil, nil, errors.New("service chain name and variable conflict")
			}
			var opt map[string]any
			if field == "service_chain_name_variable" {
				var err error
				opt, err = ipv6AclVariable(value)
				if err != nil {
					return nil, nil, err
				}
			} else {
				if family == "transport" {
					name, ok := value.(string)
					if !ok {
						return nil, nil, errors.New("invalid service chain name")
					}
					value = strings.ToUpper(name)
				}
				opt = ipv6AclOption(value)
			}
			setScalarPath(out, "accept.setServiceChain.serviceChainNumber", opt)
		case "service_chain_vpn", "service_chain_vpn_variable", "service_chain_fallback", "service_chain_fallback_variable":
			if !chainName {
				return nil, nil, errors.New("service chain options require a name")
			}
			if strings.HasSuffix(field, "_variable") {
				baseField := strings.TrimSuffix(field, "_variable")
				if input[baseField] != nil {
					return nil, nil, errors.New("service chain value and variable conflict")
				}
			}
			apiField := "vpn"
			if strings.Contains(field, "fallback") {
				apiField = "fallback"
			}
			if strings.HasSuffix(field, "_variable") {
				opt, err := ipv6AclVariable(value)
				if err != nil {
					return nil, nil, err
				}
				setScalarPath(out, "accept.setServiceChain."+apiField, opt)
			} else {
				setScalarPath(out, "accept.setServiceChain."+apiField, ipv6AclOption(value))
			}
		default:
			return nil, nil, fmt.Errorf("unsupported IPv6 ACL action %s", field)
		}
	}
	if chainName && input["service_chain_fallback"] == nil && input["service_chain_fallback_variable"] == nil {
		setScalarPath(out, "accept.setServiceChain.fallback", map[string]any{"optionType": "default", "value": false})
	}
	return out, refs, nil
}
