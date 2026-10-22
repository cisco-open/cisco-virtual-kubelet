// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
)

//go:embed definitions/service-gre-schema.json
var serviceGREInputSchema []byte

//go:embed definitions/transport-gre-schema.json
var transportGREInputSchema []byte

func compileServiceGREs(p FeatureProfile, family, key string) ([]Resource, error) {
	out := []Resource{}
	for _, vpn := range p.LANVPNs {
		rows, err := vpnRows(vpn, "gre_interfaces")
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 && family != "service" {
			return nil, errors.New("GRE interfaces require service profile")
		}
		vpnName, _ := vpn["name"].(string)
		for _, row := range rows {
			input, ok := row.(map[string]any)
			if !ok {
				return nil, errors.New("GRE interface must be object")
			}
			name, ok := input["name"].(string)
			if !ok || name == "" || len(name) > 128 {
				return nil, errors.New("GRE interface requires name")
			}
			description := ""
			if input["description"] != nil {
				description, ok = input["description"].(string)
				if !ok {
					return nil, errors.New("GRE description must be string")
				}
			}
			data, err := greData(input, serviceGREInputSchema, false)
			if err != nil {
				return nil, fmt.Errorf("GRE %s: %w", name, err)
			}
			out = append(out, Resource{Key: key + "/lan_vpns/" + vpnName + "/gre_interfaces/" + name, Name: name, Section: "feature_profiles", Kind: "parcel", Type: "service_lan_vpn_interface_gre", Collection: "/dataservice/v1/feature-profile/sdwan/service", ParentName: p.Name, ParentParcelPath: "lan/vpn", ParentParcelName: vpnName, ParcelPath: "interface/gre", Body: map[string]any{"name": name, "description": description, "data": data}})
		}
	}
	return out, nil
}

func compileTransportGREs(p FeatureProfile, family, key string) ([]Resource, error) {
	if p.WANVPN == nil {
		return nil, nil
	}
	rows, err := vpnRows(p.WANVPN, "gre_interfaces")
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 && family != "transport" {
		return nil, errors.New("WAN GRE interfaces require transport profile")
	}
	vpnName := "wan_vpn"
	if p.WANVPN["name"] != nil {
		vpnName, _ = p.WANVPN["name"].(string)
	}
	out := make([]Resource, 0, len(rows))
	for _, row := range rows {
		input, ok := row.(map[string]any)
		if !ok {
			return nil, errors.New("WAN GRE interface must be object")
		}
		name, ok := input["name"].(string)
		if !ok || name == "" || len(name) > 128 {
			return nil, errors.New("WAN GRE interface requires name")
		}
		description := ""
		if input["description"] != nil {
			description, ok = input["description"].(string)
			if !ok {
				return nil, errors.New("WAN GRE description must be string")
			}
		}
		data, err := greData(input, transportGREInputSchema, true)
		if err != nil {
			return nil, fmt.Errorf("WAN GRE %s: %w", name, err)
		}
		out = append(out, Resource{Key: key + "/wan_vpn/gre_interfaces/" + name, Name: name, Section: "feature_profiles", Kind: "parcel", Type: "transport_wan_vpn_interface_gre", Collection: "/dataservice/v1/feature-profile/sdwan/transport", ParentName: p.Name, ParentParcelPath: "wan/vpn", ParentParcelName: vpnName, ParcelPath: "interface/gre", Body: map[string]any{"name": name, "description": description, "data": data}})
	}
	return out, nil
}

func greData(input map[string]any, schemaBytes []byte, allowTracker bool) (map[string]any, error) {
	allowed := map[string]bool{"name": true, "description": true}
	if allowTracker {
		allowed["ipv4_tracker"] = true
	}
	for _, field := range []string{"interface_name", "interface_description", "shutdown", "ipv4_address", "ipv4_subnet_mask", "ipv6_address", "tunnel_source_ipv4_address", "tunnel_destination_ipv4_address", "ipv4_mtu", "ipv4_tcp_mss"} {
		allowed[field] = true
		allowed[field+"_variable"] = true
	}
	for field := range input {
		if !allowed[field] {
			return nil, fmt.Errorf("unsupported GRE field %s", field)
		}
	}
	for _, field := range []string{"interface_name", "tunnel_source_ipv4_address", "tunnel_destination_ipv4_address"} {
		if input[field] == nil && input[field+"_variable"] == nil {
			return nil, fmt.Errorf("GRE requires %s", field)
		}
	}
	basic := map[string]any{}
	for _, spec := range []struct {
		source, api  string
		defaultValue any
		present      bool
	}{{"interface_name", "ifName", nil, false}, {"interface_description", "description", nil, false}, {"shutdown", "shutdown", false, true}, {"ipv6_address", "ipv6Address", nil, false}, {"ipv4_mtu", "mtu", float64(1500), true}, {"ipv4_tcp_mss", "tcpMssAdjust", nil, false}} {
		value, err := vpnOption(input, spec.source, spec.defaultValue, spec.present)
		if err != nil {
			return nil, err
		}
		basic[spec.api] = value
	}
	address := map[string]any{}
	for _, spec := range []struct{ source, api string }{{"ipv4_address", "address"}, {"ipv4_subnet_mask", "mask"}} {
		value, err := vpnOption(input, spec.source, nil, false)
		if err != nil {
			return nil, err
		}
		address[spec.api] = value
	}
	basic["address"] = address
	basic["tunnelProtection"] = map[string]any{"optionType": "default", "value": false}
	basic["tunnelMode"] = map[string]any{"optionType": "default", "value": "ipv4"}
	source, err := vpnOption(input, "tunnel_source_ipv4_address", nil, false)
	if err != nil {
		return nil, err
	}
	basic["tunnelSourceType"] = map[string]any{"sourceIp": map[string]any{"tunnelSource": source}}
	destination, err := vpnOption(input, "tunnel_destination_ipv4_address", nil, false)
	if err != nil {
		return nil, err
	}
	basic["tunnelDestination"] = destination
	data := map[string]any{"basic": basic}
	var schema map[string]any
	var normalized any
	encoded, _ := json.Marshal(data)
	if json.Unmarshal(schemaBytes, &schema) != nil || json.Unmarshal(encoded, &normalized) != nil {
		return nil, errors.New("invalid GRE schema")
	}
	if !validScalarValue(normalized, schema) {
		for key, value := range basic {
			if !validScalarValue(value, schema["properties"].(map[string]any)["basic"].(map[string]any)["properties"].(map[string]any)[key].(map[string]any)) {
				return nil, fmt.Errorf("GRE field %s violates provider schema", key)
			}
		}
		return nil, errors.New("GRE intent violates provider schema")
	}
	return data, nil
}
