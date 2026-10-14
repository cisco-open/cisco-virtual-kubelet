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
	allowed := map[string]bool{"name": true, "description": true}
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
	data["neighbor"] = []any{}
	data["ipv6Neighbor"] = []any{}
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
