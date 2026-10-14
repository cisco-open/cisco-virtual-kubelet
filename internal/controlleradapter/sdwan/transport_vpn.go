// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
)

//go:embed definitions/transport-vpn-schemas.json
var transportVPNSchemas []byte

func vpnOption(input map[string]any, field string, defaultValue any, defaultValuePresent bool) (map[string]any, error) {
	value, hasValue := input[field]
	variable, hasVariable := input[field+"_variable"]
	if hasValue && value != nil && hasVariable && variable != nil {
		return nil, fmt.Errorf("%s value and variable conflict", field)
	}
	if hasVariable && variable != nil {
		return aclVariable(variable)
	}
	if hasValue && value != nil {
		return aclOption(value), nil
	}
	result := map[string]any{"optionType": "default"}
	if defaultValuePresent {
		result["value"] = defaultValue
	}
	return result, nil
}

func vpnOptional(input map[string]any, field string) (map[string]any, error) {
	if input[field] == nil && input[field+"_variable"] == nil {
		return nil, nil
	}
	return vpnOption(input, field, nil, false)
}

func vpnRows(input map[string]any, field string) ([]any, error) {
	if input[field] == nil {
		return []any{}, nil
	}
	rows, ok := input[field].([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array", field)
	}
	return rows, nil
}

func compileManagementVPN(p FeatureProfile, collection, key string) ([]Resource, error) {
	if p.ManagementVPN == nil {
		return nil, nil
	}
	if collection != "transport" {
		return nil, errors.New("management VPN requires a transport profile")
	}
	input := p.ManagementVPN
	for field := range input {
		switch field {
		case "name", "description", "vpn_description", "vpn_description_variable", "ipv4_primary_dns_address", "ipv4_primary_dns_address_variable", "ipv4_secondary_dns_address", "ipv4_secondary_dns_address_variable", "ipv6_primary_dns_address", "ipv6_primary_dns_address_variable", "ipv6_secondary_dns_address", "ipv6_secondary_dns_address_variable", "host_mappings", "ipv4_static_routes", "ipv6_static_routes":
		case "ethernet_interfaces":
		default:
			return nil, fmt.Errorf("unsupported management VPN field %s", field)
		}
	}
	name := "management_vpn"
	if input["name"] != nil {
		var ok bool
		name, ok = input["name"].(string)
		if !ok {
			return nil, errors.New("invalid management VPN name")
		}
	}
	if name == "" || len(name) > 128 {
		return nil, errors.New("invalid management VPN name")
	}
	description := ""
	if input["description"] != nil {
		var ok bool
		description, ok = input["description"].(string)
		if !ok {
			return nil, errors.New("invalid management VPN description")
		}
	}
	vpnName, err := vpnOption(input, "vpn_description", nil, false)
	if err != nil {
		return nil, err
	}
	data := map[string]any{"vpnId": map[string]any{"optionType": "default", "value": float64(512)}, "name": vpnName, "newHostMapping": []any{}, "ipv4Route": []any{}, "ipv6Route": []any{}}
	for _, ip := range []string{"ipv4", "ipv6"} {
		component := "dnsIpv4"
		if ip == "ipv6" {
			component = "dnsIpv6"
		}
		dns := map[string]any{}
		for _, priority := range []string{"primary", "secondary"} {
			value, err := vpnOption(input, ip+"_"+priority+"_dns_address", nil, false)
			if err != nil {
				return nil, err
			}
			api := priority + "DnsAddressIpv4"
			if ip == "ipv6" {
				api = priority + "DnsAddressIpv6"
			}
			dns[api] = value
		}
		data[component] = dns
	}
	hosts, err := vpnHostMappings(input)
	if err != nil {
		return nil, err
	}
	data["newHostMapping"] = hosts
	ipv4, err := vpnStaticRoutes(input, "ipv4")
	if err != nil {
		return nil, err
	}
	data["ipv4Route"] = ipv4
	ipv6, err := vpnStaticRoutes(input, "ipv6")
	if err != nil {
		return nil, err
	}
	data["ipv6Route"] = ipv6
	var schemas map[string]map[string]any
	encoded, _ := json.Marshal(data)
	var normalized any
	if json.Unmarshal(transportVPNSchemas, &schemas) != nil || json.Unmarshal(encoded, &normalized) != nil || !validScalarValue(normalized, schemas["management"]) {
		return nil, errors.New("management VPN intent violates provider schema")
	}
	body := map[string]any{"name": name, "description": description, "data": data}
	return []Resource{{Key: key + "/management_vpn", Name: name, Section: "feature_profiles", Kind: "parcel", Type: "transport_management_vpn", Collection: "/dataservice/v1/feature-profile/sdwan/transport", ParentName: p.Name, ParcelPath: "management/vpn", Body: body}}, nil
}

func vpnHostMappings(input map[string]any) ([]any, error) {
	rows, err := vpnRows(input, "host_mappings")
	if err != nil {
		return nil, err
	}
	out := []any{}
	for _, row := range rows {
		host, ok := row.(map[string]any)
		if !ok {
			return nil, errors.New("host mapping must be an object")
		}
		for field := range host {
			switch field {
			case "hostname", "hostname_variable", "ips", "ips_variable":
			default:
				return nil, fmt.Errorf("unsupported host mapping field %s", field)
			}
		}
		body := map[string]any{}
		for _, spec := range []struct{ public, api string }{{"hostname", "hostName"}, {"ips", "listOfIp"}} {
			opt, err := vpnOptional(host, spec.public)
			if err != nil {
				return nil, err
			}
			if opt != nil {
				body[spec.api] = opt
			}
		}
		out = append(out, body)
	}
	return out, nil
}

func vpnStaticRoutes(input map[string]any, ip string) ([]any, error) {
	rows, err := vpnRows(input, ip+"_static_routes")
	if err != nil {
		return nil, err
	}
	out := []any{}
	for _, row := range rows {
		route, ok := row.(map[string]any)
		if !ok {
			return nil, errors.New("static route must be an object")
		}
		for field := range route {
			allowed := map[string]bool{"gateway": true, "next_hops": true, "administrative_distance": ip == "ipv4", "administrative_distance_variable": ip == "ipv4", "network_address": ip == "ipv4", "network_address_variable": ip == "ipv4", "subnet_mask": ip == "ipv4", "subnet_mask_variable": ip == "ipv4", "prefix": ip == "ipv6", "prefix_variable": ip == "ipv6", "nat": ip == "ipv6", "nat_variable": ip == "ipv6"}
			if !allowed[field] {
				return nil, fmt.Errorf("unsupported %s static-route field %s", ip, field)
			}
		}
		gateway := "nexthop"
		if route["gateway"] != nil {
			var ok bool
			gateway, ok = route["gateway"].(string)
			if !ok {
				return nil, errors.New("invalid route gateway")
			}
		}
		if gateway != "nexthop" && gateway != "null0" && (ip != "ipv6" || gateway != "nat") {
			return nil, errors.New("invalid route gateway")
		}
		body := map[string]any{}
		if ip == "ipv4" {
			prefix := map[string]any{}
			for _, spec := range []struct{ public, api string }{{"network_address", "ipAddress"}, {"subnet_mask", "subnetMask"}} {
				opt, err := vpnOptional(route, spec.public)
				if err != nil {
					return nil, err
				}
				if opt != nil {
					prefix[spec.api] = opt
				}
			}
			body["prefix"] = prefix
			apiGateway := gateway
			if gateway == "nexthop" {
				apiGateway = "nextHop"
			}
			body["gateway"] = aclOption(apiGateway)
			if gateway == "null0" {
				opt, err := vpnOption(route, "administrative_distance", float64(1), true)
				if err != nil {
					return nil, err
				}
				body["distance"] = opt
			}
		} else {
			opt, err := vpnOptional(route, "prefix")
			if err != nil {
				return nil, err
			}
			if opt != nil {
				body["prefix"] = opt
			}
		}
		nextHops, err := vpnNextHops(route)
		if err != nil {
			return nil, err
		}
		if gateway == "nexthop" {
			if ip == "ipv4" {
				body["nextHop"] = nextHops
			} else {
				setScalarPath(body, "oneOfIpRoute.nextHopContainer.nextHop", nextHops)
			}
		} else if len(nextHops) > 0 {
			return nil, errors.New("next_hops require gateway nexthop")
		}
		if ip == "ipv6" {
			if gateway == "null0" {
				setScalarPath(body, "oneOfIpRoute.null0", aclOption(true))
			}
			if gateway == "nat" {
				opt, err := vpnOptional(route, "nat")
				if err != nil {
					return nil, err
				}
				if opt == nil {
					return nil, errors.New("NAT gateway requires nat value")
				}
				setScalarPath(body, "oneOfIpRoute.nat", opt)
			}
		}
		out = append(out, body)
	}
	return out, nil
}

func vpnNextHops(route map[string]any) ([]any, error) {
	rows, err := vpnRows(route, "next_hops")
	if err != nil {
		return nil, err
	}
	out := []any{}
	for _, row := range rows {
		hop, ok := row.(map[string]any)
		if !ok {
			return nil, errors.New("next hop must be an object")
		}
		for field := range hop {
			switch field {
			case "address", "address_variable", "administrative_distance", "administrative_distance_variable":
			default:
				return nil, fmt.Errorf("unsupported next-hop field %s", field)
			}
		}
		body := map[string]any{}
		address, err := vpnOptional(hop, "address")
		if err != nil {
			return nil, err
		}
		if address != nil {
			body["address"] = address
		}
		distance, err := vpnOption(hop, "administrative_distance", float64(1), true)
		if err != nil {
			return nil, err
		}
		body["distance"] = distance
		out = append(out, body)
	}
	return out, nil
}

func compileWANVPN(p FeatureProfile, collection, key string) ([]Resource, error) {
	if p.WANVPN == nil {
		return nil, nil
	}
	if collection != "transport" {
		return nil, errors.New("WAN VPN requires a transport profile")
	}
	input := p.WANVPN
	for _, unsupported := range []string{"vpn_description", "vpn_description_variable"} {
		if input[unsupported] != nil {
			return nil, fmt.Errorf("unsupported WAN VPN field %s", unsupported)
		}
	}
	if routes, err := vpnRows(input, "ipv6_static_routes"); err != nil {
		return nil, err
	} else {
		for _, route := range routes {
			if entry, ok := route.(map[string]any); ok && entry["nat_variable"] != nil {
				return nil, errors.New("WAN VPN IPv6 NAT variable is not in the pinned model")
			}
		}
	}
	base := make(map[string]any, len(input))
	for field, value := range input {
		switch field {
		case "enhance_ecmp_keying", "enhance_ecmp_keying_variable", "nat_64_v4_pools", "services", "ospf", "bgp", "gre_interfaces", "ipsec_interfaces", "ethernet_interfaces", "cellular_interfaces":
		default:
			base[field] = value
		}
	}
	clone := p
	clone.ManagementVPN = base
	resources, err := compileManagementVPN(clone, collection, key)
	if err != nil {
		return nil, err
	}
	r := resources[0]
	data := r.Body["data"].(map[string]any)
	delete(data, "name")
	for _, family := range []struct{ source, api, field string }{{"ipv4", "dnsIpv4", "secondaryDnsAddressIpv4"}, {"ipv6", "dnsIpv6", "secondaryDnsAddressIpv6"}} {
		if input[family.source+"_secondary_dns_address"] == nil && input[family.source+"_secondary_dns_address_variable"] == nil {
			delete(data[family.api].(map[string]any), family.field)
		}
	}
	data["vpnId"] = map[string]any{"optionType": "global", "value": float64(0)}
	ecmp, err := vpnOption(input, "enhance_ecmp_keying", false, true)
	if err != nil {
		return nil, err
	}
	data["enhanceEcmpKeying"] = ecmp
	pools, err := vpnRows(input, "nat_64_v4_pools")
	if err != nil {
		return nil, err
	}
	data["nat64V4Pool"] = []any{}
	for _, entry := range pools {
		pool, ok := entry.(map[string]any)
		if !ok {
			return nil, errors.New("NAT64 pool must be an object")
		}
		body := map[string]any{}
		for field, api := range map[string]string{"name": "nat64V4PoolName", "overload": "nat64V4PoolOverload", "range_start": "nat64V4PoolRangeStart", "range_end": "nat64V4PoolRangeEnd"} {
			option, err := vpnOptional(pool, field)
			if err != nil {
				return nil, err
			}
			if option != nil {
				body[api] = option
			}
		}
		for field := range pool {
			if field != "name" && field != "name_variable" && field != "overload" && field != "overload_variable" && field != "range_start" && field != "range_start_variable" && field != "range_end" && field != "range_end_variable" {
				return nil, fmt.Errorf("unsupported NAT64 pool field %s", field)
			}
		}
		data["nat64V4Pool"] = append(data["nat64V4Pool"].([]any), body)
	}
	services, err := vpnRows(input, "services")
	if err != nil {
		return nil, err
	}
	data["service"] = []any{}
	for _, value := range services {
		service, ok := value.(string)
		if !ok || service != "TE" {
			return nil, errors.New("WAN VPN service must be TE")
		}
		data["service"] = append(data["service"].([]any), map[string]any{"serviceType": aclOption(service)})
	}
	var schemas map[string]map[string]any
	encoded, _ := json.Marshal(data)
	var normalized any
	if json.Unmarshal(transportVPNSchemas, &schemas) != nil || json.Unmarshal(encoded, &normalized) != nil || !validScalarValue(normalized, schemas["wan"]) {
		for field, value := range data {
			if property, ok := schemas["wan"]["properties"].(map[string]any)[field].(map[string]any); ok && !validScalarValue(value, property) {
				if object, ok := value.(map[string]any); ok {
					for child, childValue := range object {
						if childSchema, ok := property["properties"].(map[string]any)[child].(map[string]any); ok && !validScalarValue(childValue, childSchema) {
							return nil, fmt.Errorf("WAN VPN %s.%s violates provider schema: %v", field, child, childValue)
						}
					}
				}
				return nil, fmt.Errorf("WAN VPN %s violates provider schema: %v", field, value)
			}
		}
		return nil, errors.New("WAN VPN intent violates provider schema")
	}
	r.Key = key + "/wan_vpn"
	r.Type = "transport_wan_vpn"
	r.ParcelPath = "wan/vpn"
	return []Resource{r}, nil
}
