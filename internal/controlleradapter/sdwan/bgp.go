// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
)

//go:embed definitions/bgp-schemas.json
var bgpSchemas []byte

func compileBGPFeatures(p FeatureProfile, family, key string) ([]Resource, error) {
	if len(p.BGP) > 0 && family != "service" && family != "transport" {
		return nil, errors.New("BGP requires service or transport profile")
	}
	out := make([]Resource, 0, len(p.BGP))
	for _, input := range p.BGP {
		name, ok := input["name"].(string)
		if !ok || name == "" || len(name) > 128 {
			return nil, errors.New("BGP feature requires name")
		}
		description := ""
		if input["description"] != nil {
			description, ok = input["description"].(string)
			if !ok {
				return nil, errors.New("BGP description must be string")
			}
		}
		data, err := bgpData(input, family)
		if err != nil {
			return nil, fmt.Errorf("BGP %s: %w", name, err)
		}
		out = append(out, Resource{Key: key + "/bgp_features/" + name, Name: name, Section: "feature_profiles", Kind: "parcel", Type: family + "_routing_bgp", Collection: "/dataservice/v1/feature-profile/sdwan/" + family, ParentName: p.Name, ParcelPath: "routing/bgp", Body: map[string]any{"name": name, "description": description, "data": data}})
	}
	return out, nil
}

func bgpData(input map[string]any, family string) (map[string]any, error) {
	fields := []struct {
		source, api  string
		defaultValue any
		present      bool
	}{
		{"as_number", "asNum", nil, false}, {"router_id", "routerId", nil, false},
		{"external_routes_distance", "external", float64(20), true}, {"internal_routes_distance", "internal", float64(200), true}, {"local_routes_distance", "local", float64(20), true},
		{"keepalive_time", "keepalive", float64(60), true}, {"hold_time", "holdtime", float64(180), true},
		{"always_compare_med", "alwaysCompare", false, true}, {"deterministic_med", "deterministic", false, true}, {"missing_as_worst", "missingAsWorst", false, true}, {"compare_router_id", "compareRouterId", false, true}, {"multipath_relax", "multipathRelax", false, true},
		{"propagate_aspath", "propagateAspath", false, true}, {"propagate_community", "propagateCommunity", false, true},
	}
	allowed := map[string]bool{"name": true, "description": true, "ipv4_neighbors": true, "ipv6_neighbors": true, "ipv4_networks": true, "ipv6_networks": true, "ipv4_default_originate": true, "ipv4_default_originate_variable": true, "ipv6_default_originate": true, "ipv6_default_originate_variable": true}
	for _, spec := range fields {
		allowed[spec.source] = true
		allowed[spec.source+"_variable"] = true
	}
	for field := range input {
		if !allowed[field] {
			return nil, fmt.Errorf("unsupported BGP field %s", field)
		}
	}
	if input["as_number"] == nil && input["as_number_variable"] == nil {
		return nil, errors.New("BGP requires as_number")
	}
	data := map[string]any{}
	for _, spec := range fields {
		value, err := vpnOption(input, spec.source, spec.defaultValue, spec.present)
		if err != nil {
			return nil, err
		}
		data[spec.api] = value
	}
	for _, spec := range []struct{ source, api string }{{"ipv4_neighbors", "neighbor"}, {"ipv6_neighbors", "ipv6Neighbor"}} {
		rows, err := vpnRows(input, spec.source)
		if err != nil {
			return nil, err
		}
		items := []any{}
		for _, row := range rows {
			neighbor, ok := row.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s neighbor must be object", spec.source)
			}
			item, err := bgpNeighborData(neighbor)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		data[spec.api] = items
	}
	for _, spec := range []struct{ source, api, networkKey, originate string }{{"ipv4_networks", "addressFamily", "network", "ipv4_default_originate"}, {"ipv6_networks", "ipv6AddressFamily", "ipv6Network", "ipv6_default_originate"}} {
		rows, err := vpnRows(input, spec.source)
		if err != nil {
			return nil, err
		}
		items := []any{}
		for _, row := range rows {
			entry, ok := row.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s entry must be object", spec.source)
			}
			item, err := bgpNetworkData(entry, spec.source == "ipv6_networks")
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		originate, err := vpnOption(input, spec.originate, false, true)
		if err != nil {
			return nil, err
		}
		data[spec.api] = map[string]any{spec.networkKey: items, "originate": originate, "name": map[string]any{"optionType": "default"}}
	}
	var schemas map[string]map[string]any
	var normalized any
	encoded, _ := json.Marshal(data)
	if json.Unmarshal(bgpSchemas, &schemas) != nil || json.Unmarshal(encoded, &normalized) != nil {
		return nil, errors.New("invalid BGP schema")
	}
	schema := schemas[family]
	if !validScalarValue(normalized, schema) {
		for key, value := range normalized.(map[string]any) {
			if !validScalarValue(value, schema["properties"].(map[string]any)[key].(map[string]any)) {
				return nil, fmt.Errorf("BGP field %s violates provider schema", key)
			}
		}
		return nil, errors.New("BGP intent violates provider schema")
	}
	return data, nil
}

func bgpNetworkData(input map[string]any, ipv6 bool) (map[string]any, error) {
	if ipv6 {
		for field := range input {
			if field != "prefix" && field != "prefix_variable" {
				return nil, fmt.Errorf("unsupported IPv6 BGP network field %s", field)
			}
		}
		value, err := vpnOptional(input, "prefix")
		if err != nil {
			return nil, err
		}
		if value == nil {
			return nil, errors.New("IPv6 BGP network requires prefix")
		}
		return map[string]any{"prefix": value}, nil
	}
	for field := range input {
		if field != "network_address" && field != "network_address_variable" && field != "subnet_mask" && field != "subnet_mask_variable" {
			return nil, fmt.Errorf("unsupported IPv4 BGP network field %s", field)
		}
	}
	prefix := map[string]any{}
	for _, spec := range []struct{ source, api string }{{"network_address", "address"}, {"subnet_mask", "mask"}} {
		value, err := vpnOptional(input, spec.source)
		if err != nil {
			return nil, err
		}
		if value == nil {
			return nil, fmt.Errorf("IPv4 BGP network requires %s", spec.source)
		}
		prefix[spec.api] = value
	}
	return map[string]any{"prefix": prefix}, nil
}

func bgpNeighborData(input map[string]any) (map[string]any, error) {
	fields := []struct{ source, api string }{{"address", "address"}, {"remote_as", "remoteAs"}, {"description", "description"}, {"shutdown", "shutdown"}, {"local_as", "localAs"}, {"keepalive_time", "keepalive"}, {"hold_time", "holdtime"}, {"next_hop_self", "nextHopSelf"}, {"send_community", "sendCommunity"}, {"send_extended_community", "sendExtCommunity"}, {"ebgp_multihop", "ebgpMultihop"}, {"password", "password"}}
	allowed := map[string]bool{}
	item := map[string]any{}
	for _, spec := range fields {
		allowed[spec.source] = true
		allowed[spec.source+"_variable"] = true
		value, err := vpnOptional(input, spec.source)
		if err != nil {
			return nil, err
		}
		if value != nil {
			item[spec.api] = value
		}
	}
	for field := range input {
		if !allowed[field] {
			return nil, fmt.Errorf("unsupported BGP neighbor field %s", field)
		}
	}
	if item["address"] == nil || item["remoteAs"] == nil {
		return nil, errors.New("BGP neighbor requires address and remote_as")
	}
	return item, nil
}
