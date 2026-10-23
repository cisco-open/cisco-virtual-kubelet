// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

//go:embed definitions/service-dual-router-ha-schema.json
var dualRouterHASchema []byte

func compileDualRouterHA(p FeatureProfile, collection, key string) ([]Resource, error) {
	if len(p.DualRouterHA) == 0 {
		return nil, nil
	}
	if collection != "service" {
		return nil, errors.New("dual-router HA requires service profile")
	}
	declared := map[string]bool{}
	for _, vpn := range p.LANVPNs {
		if name, ok := vpn["name"].(string); ok && name != "" {
			declared[name] = true
		}
	}
	out := []Resource{}
	for _, input := range p.DualRouterHA {
		name, ok := input["name"].(string)
		if !ok || name == "" || len(name) > 128 {
			return nil, errors.New("dual-router HA requires name")
		}
		description := ""
		if input["description"] != nil {
			var ok bool
			description, ok = input["description"].(string)
			if !ok {
				return nil, errors.New("dual-router HA description must be string")
			}
		}
		for field := range input {
			switch field {
			case "name", "description", "enable_optimize_paths", "redundancy_groups":
			default:
				return nil, fmt.Errorf("unsupported dual-router HA field %s", field)
			}
		}
		optimize, err := vpnOption(input, "enable_optimize_paths", true, true)
		if err != nil {
			return nil, err
		}
		rows, err := vpnRows(input, "redundancy_groups")
		if err != nil {
			return nil, err
		}
		groups, refs := []any{}, []ParcelReference{}
		for i, row := range rows {
			group, ok := row.(map[string]any)
			if !ok {
				return nil, errors.New("redundancy group must be object")
			}
			for field := range group {
				switch field {
				case "group_id", "tag_name", "vpn_names":
				default:
					return nil, fmt.Errorf("unsupported redundancy group field %s", field)
				}
			}
			groupID, ok := group["group_id"].(float64)
			if !ok {
				return nil, errors.New("redundancy group requires numeric group_id")
			}
			body := map[string]any{"groupId": groupID}
			if group["tag_name"] != nil {
				body["tagName"] = group["tag_name"]
			}
			vpnNames, err := vpnRows(group, "vpn_names")
			if err != nil {
				return nil, err
			}
			vpnIDs := []any{}
			for j, value := range vpnNames {
				vpnName, ok := value.(string)
				if !ok || !declared[vpnName] {
					return nil, errors.New("dual-router HA VPN must be declared in same profile")
				}
				vpnIDs = append(vpnIDs, map[string]any{"refId": aclOption(parcelPlaceholder(vpnName))})
				refs = append(refs, ParcelReference{Kind: "service_lan_vpn", Name: vpnName, ParcelPath: "lan/vpn", BodyPath: []string{"data", "redundancyGroups", strconv.Itoa(i), "vpnIds", strconv.Itoa(j), "refId", "value"}})
			}
			body["vpnIds"] = vpnIDs
			groups = append(groups, body)
		}
		data := map[string]any{"redundancyGroups": groups, "enableOptimizePaths": optimize}
		var schema map[string]any
		encoded, _ := json.Marshal(data)
		var normalized any
		if json.Unmarshal(dualRouterHASchema, &schema) != nil || json.Unmarshal(encoded, &normalized) != nil || !validScalarValue(normalized, schema) {
			return nil, errors.New("dual-router HA intent violates provider schema")
		}
		out = append(out, Resource{Key: key + "/dual_router_ha_features/" + name, Name: name, Section: "feature_profiles", Kind: "parcel", Type: "service_dual_router_ha", Collection: "/dataservice/v1/feature-profile/sdwan/service", ParentName: p.Name, ParcelPath: "dual-router-ha", ParcelRefs: refs, Body: map[string]any{"name": name, "description": description, "data": data}})
	}
	return out, nil
}
