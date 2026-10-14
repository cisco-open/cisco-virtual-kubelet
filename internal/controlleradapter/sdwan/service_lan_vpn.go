// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
)

//go:embed definitions/service-lan-vpn-schema.json
var serviceLANVPNSchema []byte

func compileServiceLANVPNs(p FeatureProfile, collection, key string) ([]Resource, error) {
	if len(p.LANVPNs) == 0 {
		return nil, nil
	}
	if collection != "service" {
		return nil, errors.New("LAN VPNs require a service profile")
	}
	out := make([]Resource, 0, len(p.LANVPNs))
	for _, input := range p.LANVPNs {
		name, ok := input["name"].(string)
		if !ok || name == "" || len(name) > 128 {
			return nil, errors.New("LAN VPN requires name")
		}
		description := ""
		if input["description"] != nil {
			var ok bool
			description, ok = input["description"].(string)
			if !ok {
				return nil, errors.New("LAN VPN description must be a string")
			}
		}
		data, err := serviceLANVPNData(input)
		if err != nil {
			return nil, fmt.Errorf("LAN VPN %s: %w", name, err)
		}
		out = append(out, Resource{Key: key + "/lan_vpns/" + name, Name: name, Section: "feature_profiles", Kind: "parcel", Type: "service_lan_vpn", Collection: "/dataservice/v1/feature-profile/sdwan/service", ParentName: p.Name, ParcelPath: "lan/vpn", Body: map[string]any{"name": name, "description": description, "data": data}})
	}
	return out, nil
}

func serviceLANVPNData(input map[string]any) (map[string]any, error) {
	allowed := map[string]bool{"name": true, "description": true, "vpn_id": true, "vpn_id_variable": true, "vpn_name": true, "vpn_name_variable": true, "sdwan_remote_access": true, "host_mappings": true, "ipv4_static_routes": true, "ipv6_static_routes": true}
	for _, field := range []string{"ipv4_omp_admin_distance", "ipv6_omp_admin_distance", "ipv4_primary_dns_address", "ipv4_secondary_dns_address", "ipv6_primary_dns_address", "ipv6_secondary_dns_address"} {
		allowed[field] = true
		allowed[field+"_variable"] = true
	}
	for field := range input {
		if !allowed[field] {
			return nil, fmt.Errorf("unsupported LAN VPN field %s", field)
		}
	}
	if input["vpn_id"] == nil && input["vpn_id_variable"] == nil {
		return nil, errors.New("LAN VPN requires vpn_id or vpn_id_variable")
	}
	vpnID, err := vpnOptional(input, "vpn_id")
	if err != nil {
		return nil, err
	}
	vpnName, err := vpnOption(input, "vpn_name", nil, false)
	if err != nil {
		return nil, err
	}
	remoteAccess, err := vpnOption(input, "sdwan_remote_access", false, true)
	if err != nil {
		return nil, err
	}
	data := map[string]any{"vpnId": vpnID, "name": vpnName, "enableSdra": remoteAccess}
	for _, spec := range []struct{ source, api string }{{"ipv4_omp_admin_distance", "ompAdminDistance"}, {"ipv6_omp_admin_distance", "ompAdminDistanceIpv6"}} {
		option, err := vpnOption(input, spec.source, nil, false)
		if err != nil {
			return nil, err
		}
		data[spec.api] = option
	}
	for _, ip := range []string{"ipv4", "ipv6"} {
		dns := map[string]any{}
		for _, priority := range []string{"primary", "secondary"} {
			field := ip + "_" + priority + "_dns_address"
			if priority == "secondary" && input[field] == nil && input[field+"_variable"] == nil {
				continue
			}
			option, err := vpnOption(input, field, nil, false)
			if err != nil {
				return nil, err
			}
			api := priority + "DnsAddressIpv4"
			if ip == "ipv6" {
				api = priority + "DnsAddressIpv6"
			}
			dns[api] = option
		}
		api := "dnsIpv4"
		if ip == "ipv6" {
			api = "dnsIpv6"
		}
		data[api] = dns
	}
	hosts, err := vpnHostMappings(input)
	if err != nil {
		return nil, err
	}
	data["newHostMapping"] = hosts
	ipv4, err := serviceLANIPv4Routes(input)
	if err != nil {
		return nil, err
	}
	data["ipv4Route"] = ipv4
	ipv6, err := vpnStaticRoutes(input, "ipv6")
	if err != nil {
		return nil, err
	}
	data["ipv6Route"] = ipv6
	var schema map[string]any
	encoded, _ := json.Marshal(data)
	var normalized any
	if json.Unmarshal(serviceLANVPNSchema, &schema) != nil || json.Unmarshal(encoded, &normalized) != nil || !validScalarValue(normalized, schema) {
		return nil, errors.New("LAN VPN intent violates provider schema")
	}
	return data, nil
}

func serviceLANIPv4Routes(input map[string]any) ([]any, error) {
	rows, err := vpnRows(input, "ipv4_static_routes")
	if err != nil {
		return nil, err
	}
	out := []any{}
	for _, row := range rows {
		route, ok := row.(map[string]any)
		if !ok {
			return nil, errors.New("IPv4 route must be an object")
		}
		for field := range route {
			switch field {
			case "network_address", "network_address_variable", "subnet_mask", "subnet_mask_variable", "gateway", "next_hops", "administrative_distance", "administrative_distance_variable":
			default:
				return nil, fmt.Errorf("unsupported LAN IPv4 route field %s", field)
			}
		}
		gateway := "nexthop"
		if route["gateway"] != nil {
			var ok bool
			gateway, ok = route["gateway"].(string)
			if !ok {
				return nil, errors.New("invalid LAN route gateway")
			}
		}
		if gateway != "nexthop" && gateway != "null0" {
			return nil, errors.New("unsupported LAN route gateway")
		}
		prefix := map[string]any{}
		for _, spec := range []struct{ source, api string }{{"network_address", "ipAddress"}, {"subnet_mask", "subnetMask"}} {
			option, err := vpnOption(route, spec.source, nil, false)
			if err != nil {
				return nil, err
			}
			prefix[spec.api] = option
		}
		body := map[string]any{"prefix": prefix}
		if gateway == "nexthop" {
			hops, err := vpnNextHops(route)
			if err != nil {
				return nil, err
			}
			if len(hops) == 0 {
				return nil, errors.New("LAN next-hop route requires next_hops")
			}
			body["oneOfIpRoute"] = map[string]any{"nextHopContainer": map[string]any{"nextHop": hops}}
		} else {
			if route["next_hops"] != nil {
				return nil, errors.New("null0 route cannot have next_hops")
			}
			distance, err := vpnOption(route, "administrative_distance", float64(1), true)
			if err != nil {
				return nil, err
			}
			body["oneOfIpRoute"] = map[string]any{"null0": aclOption(true), "distance": distance}
		}
		out = append(out, body)
	}
	return out, nil
}
