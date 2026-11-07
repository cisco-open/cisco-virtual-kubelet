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

//go:embed definitions/ipv4-acl-schemas.json
var ipv4ACLSchemas []byte

func compileIPv4ACLs(p FeatureProfile, family, key string) ([]Resource, error) {
	if len(p.IPv4ACLs) == 0 {
		return nil, nil
	}
	if family != "service" && family != "transport" {
		return nil, errors.New("IPv4 ACLs require a service or transport profile")
	}
	out := []Resource{}
	seen := map[string]bool{}
	for _, input := range p.IPv4ACLs {
		name, body, refs, err := ipv4ACLData(input, family)
		if err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, errors.New("duplicate IPv4 ACL name")
		}
		seen[name] = true
		out = append(out, Resource{Key: key + "/ipv4_acls/" + name, Name: name, Section: "feature_profiles", Kind: "parcel", Type: family + "_ipv4_acl", Collection: "/dataservice/v1/feature-profile/sdwan/" + family, ParentName: p.Name, ParcelPath: "ipv4-acl", ParcelRefs: refs, Body: body})
	}
	return out, nil
}

func ipv4ACLData(input map[string]any, family string) (string, map[string]any, []ParcelReference, error) {
	for field := range input {
		switch field {
		case "name", "description", "default_action", "sequences":
		default:
			return "", nil, nil, fmt.Errorf("unsupported IPv4 ACL field %s", field)
		}
	}
	name, ok := input["name"].(string)
	if !ok || name == "" || len(name) > 128 {
		return "", nil, nil, errors.New("invalid IPv4 ACL name")
	}
	description := ""
	if input["description"] != nil {
		var ok bool
		description, ok = input["description"].(string)
		if !ok {
			return "", nil, nil, errors.New("invalid IPv4 ACL description")
		}
	}
	defaultAction, hasDefault := input["default_action"].(string)
	if !hasDefault {
		if family == "transport" {
			return "", nil, nil, errors.New("transport IPv4 ACL default_action is required")
		}
		defaultAction = "drop"
	}
	if defaultAction != "drop" && defaultAction != "accept" {
		return "", nil, nil, errors.New("invalid IPv4 ACL default_action")
	}
	defaultOption := map[string]any{"optionType": "global", "value": defaultAction}
	data := map[string]any{"defaultAction": defaultOption, "sequences": []any{}}
	refs := []ParcelReference{}
	if raw := input["sequences"]; raw != nil {
		sequences, ok := raw.([]any)
		if !ok {
			return "", nil, nil, errors.New("IPv4 ACL sequences must be an array")
		}
		out := make([]any, 0, len(sequences))
		for i, item := range sequences {
			seq, ok := item.(map[string]any)
			if !ok {
				return "", nil, nil, errors.New("IPv4 ACL sequence must be an object")
			}
			for field := range seq {
				switch field {
				case "id", "name", "base_action", "actions", "match_entries":
				default:
					return "", nil, nil, fmt.Errorf("unsupported IPv4 ACL sequence field %s", field)
				}
			}
			id, ok := seq["id"].(float64)
			if !ok {
				return "", nil, nil, errors.New("IPv4 ACL sequence id is required")
			}
			seqName := "acl"
			if seq["name"] != nil {
				var ok bool
				seqName, ok = seq["name"].(string)
				if !ok {
					return "", nil, nil, errors.New("invalid IPv4 ACL sequence name")
				}
			}
			base, ok := seq["base_action"].(string)
			if !ok || (base != "accept" && base != "drop") {
				return "", nil, nil, errors.New("invalid IPv4 ACL base_action")
			}
			entry := map[string]any{"sequenceId": aclOption(id), "sequenceName": aclOption(seqName)}
			actions, ok := seq["actions"].(map[string]any)
			if seq["actions"] != nil && !ok {
				return "", nil, nil, errors.New("IPv4 ACL actions must be an object")
			}
			if len(actions) == 0 {
				entry["baseAction"] = aclOption(base)
			} else {
				act, more, err := ipv4ACLActions(actions, base, family, i)
				if err != nil {
					return "", nil, nil, err
				}
				entry["actions"] = []any{act}
				refs = append(refs, more...)
			}
			matches, ok := seq["match_entries"].(map[string]any)
			if seq["match_entries"] != nil && !ok {
				return "", nil, nil, errors.New("IPv4 ACL match_entries must be an object")
			}
			if len(matches) > 0 {
				match, more, err := ipv4ACLMatch(matches, i)
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
	if json.Unmarshal(ipv4ACLSchemas, &schemas) != nil || json.Unmarshal(encoded, &normalized) != nil || !validScalarValue(normalized, schemas[family]) {
		return "", nil, nil, errors.New("IPv4 ACL intent violates provider schema")
	}
	return name, map[string]any{"name": name, "description": description, "data": data}, refs, nil
}

func aclOption(value any) map[string]any {
	return map[string]any{"optionType": "global", "value": value}
}
func aclVariable(value any) (map[string]any, error) {
	name, ok := value.(string)
	if !ok || !validParcelVariable.MatchString(name) {
		return nil, errors.New("invalid ACL variable")
	}
	return map[string]any{"optionType": "variable", "value": "{{" + name + "}}"}, nil
}
func aclReference(name any, kind, path string, bodyPath []string) (map[string]any, ParcelReference, error) {
	text, ok := name.(string)
	if !ok || text == "" || len(text) > 128 {
		return nil, ParcelReference{}, errors.New("invalid ACL policy-object reference")
	}
	ref := ParcelReference{Kind: kind, Name: text, ParcelPath: path, Collection: "/dataservice/v1/feature-profile/sdwan/policy-object", ParentName: "$policy_objects", BodyPath: bodyPath}
	return aclOption(parcelPlaceholder(text)), ref, nil
}

func ipv4ACLMatch(input map[string]any, index int) (map[string]any, []ParcelReference, error) {
	out := map[string]any{}
	refs := []ParcelReference{}
	for _, direction := range []string{"source", "destination"} {
		count := 0
		for _, suffix := range []string{"data_prefix", "data_prefix_variable", "data_prefix_list"} {
			if input[direction+"_"+suffix] != nil {
				count++
			}
		}
		if count > 1 {
			return nil, nil, errors.New("conflicting IPv4 ACL prefix inputs")
		}
	}
	for field, value := range input {
		switch field {
		case "source_data_prefix", "destination_data_prefix", "source_data_prefix_variable", "destination_data_prefix_variable", "source_data_prefix_list", "destination_data_prefix_list":
			direction := "source"
			if strings.HasPrefix(field, "destination_") {
				direction = "destination"
			}
			api := direction + "DataPrefix"
			if strings.HasSuffix(field, "_list") {
				path := []string{"data", "sequences", fmt.Sprint(index), "matchEntries", "0", api, api + "List", "refId", "value"}
				opt, ref, err := aclReference(value, "policy_object_data_ipv4_prefix_list", "data-prefix", path)
				if err != nil {
					return nil, nil, err
				}
				setScalarPath(out, api+"."+api+"List.refId", opt)
				refs = append(refs, ref)
			} else if strings.HasSuffix(field, "_variable") {
				opt, err := aclVariable(value)
				if err != nil {
					return nil, nil, err
				}
				setScalarPath(out, api+"."+direction+"IpPrefix", opt)
			} else {
				setScalarPath(out, api+"."+direction+"IpPrefix", aclOption(value))
			}
		case "dscps":
			out["dscp"] = aclOption(value)
		case "icmp_messages":
			out["icmpMsg"] = aclOption(value)
		case "packet_length":
			out["packetLength"] = aclOption(value)
		case "protocols":
			out["protocol"] = aclOption(value)
		case "tcp_state":
			out["tcp"] = aclOption(value)
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
				rows = append(rows, map[string]any{api: aclOption(port)})
			}
			out[fieldToAPI(field)] = rows
		default:
			return nil, nil, fmt.Errorf("unsupported IPv4 ACL match field %s", field)
		}
	}
	return out, refs, nil
}
func fieldToAPI(field string) string {
	if field == "source_ports" {
		return "sourcePorts"
	}
	return "destinationPorts"
}

func ipv4ACLActions(input map[string]any, base, family string, index int) (map[string]any, []ParcelReference, error) {
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
			setScalarPath(out, api+".counterName", aclOption(value))
		case "log":
			setScalarPath(out, api+".log", aclOption(value))
		case "mirror", "policer":
			kind := "policy_object_" + field
			path := field
			bodyPath := []string{"data", "sequences", fmt.Sprint(index), "actions", "0", "accept", field, "refId", "value"}
			opt, ref, err := aclReference(value, kind, path, bodyPath)
			if err != nil {
				return nil, nil, err
			}
			setScalarPath(out, "accept."+field+".refId", opt)
			refs = append(refs, ref)
		case "dscp":
			setScalarPath(out, "accept.setDscp", aclOption(value))
		case "ipv4_next_hop":
			setScalarPath(out, "accept.setNextHop", aclOption(value))
		case "service_chain_name", "service_chain_name_variable":
			if input["service_chain_name"] != nil && input["service_chain_name_variable"] != nil {
				return nil, nil, errors.New("service chain name and variable conflict")
			}
			var opt map[string]any
			if field == "service_chain_name_variable" {
				var err error
				opt, err = aclVariable(value)
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
				opt = aclOption(value)
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
				opt, err := aclVariable(value)
				if err != nil {
					return nil, nil, err
				}
				setScalarPath(out, "accept.setServiceChain."+apiField, opt)
			} else {
				setScalarPath(out, "accept.setServiceChain."+apiField, aclOption(value))
			}
		default:
			return nil, nil, fmt.Errorf("unsupported IPv4 ACL action %s", field)
		}
	}
	if chainName && input["service_chain_fallback"] == nil && input["service_chain_fallback_variable"] == nil {
		setScalarPath(out, "accept.setServiceChain.fallback", map[string]any{"optionType": "default", "value": false})
	}
	return out, refs, nil
}
