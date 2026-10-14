// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
)

//go:embed definitions/service-svi-schema.json
var serviceSVISchema []byte

func compileServiceSVIs(p FeatureProfile, family, key string) ([]Resource, error) {
	out := []Resource{}
	for _, vpn := range p.LANVPNs {
		rows, err := vpnRows(vpn, "svi_interfaces")
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 && family != "service" {
			return nil, errors.New("SVI interfaces require service profile")
		}
		vpnName, _ := vpn["name"].(string)
		for _, row := range rows {
			input, ok := row.(map[string]any)
			if !ok {
				return nil, errors.New("SVI interface must be object")
			}
			name, ok := input["name"].(string)
			if !ok || name == "" || len(name) > 128 {
				return nil, errors.New("SVI interface requires name")
			}
			description := ""
			if input["description"] != nil {
				var ok bool
				description, ok = input["description"].(string)
				if !ok {
					return nil, errors.New("SVI description must be string")
				}
			}
			data, err := serviceSVIData(input)
			if err != nil {
				return nil, fmt.Errorf("SVI %s: %w", name, err)
			}
			out = append(out, Resource{Key: key + "/lan_vpns/" + vpnName + "/svi_interfaces/" + name, Name: name, Section: "feature_profiles", Kind: "parcel", Type: "service_lan_vpn_interface_svi", Collection: "/dataservice/v1/feature-profile/sdwan/service", ParentName: p.Name, ParentParcelPath: "lan/vpn", ParentParcelName: vpnName, ParcelPath: "interface/svi", Body: map[string]any{"name": name, "description": description, "data": data}})
		}
	}
	return out, nil
}

func serviceSVIData(input map[string]any) (map[string]any, error) {
	fields := []struct {
		source, api  string
		defaultValue any
		present      bool
	}{
		{"shutdown", "shutdown", true, true}, {"interface_name", "interfaceName", nil, false}, {"interface_description", "description", nil, false}, {"interface_mtu", "ifMtu", float64(1500), true}, {"ip_mtu", "ipMtu", float64(1500), true}, {"ipv6_address", "addressV6", nil, false}, {"tcp_mss", "tcpMss", nil, false}, {"arp_timeout", "arpTimeout", float64(1200), true}, {"ip_directed_broadcast", "ipDirectedBroadcast", false, true}, {"icmp_redirect_disable", "icmpRedirectDisable", true, true}, {"ipv4_dhcp_helpers", "dhcpHelperV4", nil, false},
	}
	allowed := map[string]bool{"name": true, "description": true, "ipv4_address": true, "ipv4_address_variable": true, "ipv4_subnet_mask": true, "ipv4_subnet_mask_variable": true, "ipv4_secondary_addresses": true, "ipv6_secondary_addresses": true, "arp_entries": true}
	for _, spec := range fields {
		allowed[spec.source] = true
		allowed[spec.source+"_variable"] = true
	}
	for field := range input {
		if !allowed[field] {
			return nil, fmt.Errorf("unsupported SVI field %s", field)
		}
	}
	if input["interface_name"] == nil && input["interface_name_variable"] == nil {
		return nil, errors.New("SVI requires interface_name")
	}
	data := map[string]any{}
	for _, spec := range fields[:5] {
		option, err := vpnOption(input, spec.source, spec.defaultValue, spec.present)
		if err != nil {
			return nil, err
		}
		data[spec.api] = option
	}
	ipv4 := map[string]any{}
	address := map[string]any{}
	for _, spec := range []struct{ source, api string }{{"ipv4_address", "ipAddress"}, {"ipv4_subnet_mask", "subnetMask"}} {
		option, err := vpnOptional(input, spec.source)
		if err != nil {
			return nil, err
		}
		if option != nil {
			address[spec.api] = option
		}
	}
	ipv4["addressV4"] = address
	secondary, err := managementInterfaceRows(input, "ipv4_secondary_addresses", []struct{ source, api string }{{"address", "ipAddress"}, {"subnet_mask", "subnetMask"}})
	if err != nil {
		return nil, err
	}
	ipv4["secondaryAddressV4"] = secondary
	dhcp, err := vpnOption(input, "ipv4_dhcp_helpers", nil, false)
	if err != nil {
		return nil, err
	}
	ipv4["dhcpHelperV4"] = dhcp
	data["ipv4"] = ipv4
	ipv6Address, err := vpnOption(input, "ipv6_address", nil, false)
	if err != nil {
		return nil, err
	}
	ipv6Secondary, err := managementInterfaceRows(input, "ipv6_secondary_addresses", []struct{ source, api string }{{"address", "address"}})
	if err != nil {
		return nil, err
	}
	data["ipv6"] = map[string]any{"addressV6": ipv6Address, "secondaryAddressV6": ipv6Secondary, "dhcpHelperV6": []any{}}
	arp, err := managementInterfaceRows(input, "arp_entries", []struct{ source, api string }{{"ip_address", "ipAddress"}, {"mac_address", "macAddress"}})
	if err != nil {
		return nil, err
	}
	data["arp"] = arp
	data["vrrp"] = []any{}
	data["vrrpIpv6"] = []any{}
	data["dhcpClientV6"] = map[string]any{"optionType": "default", "value": false}
	advanced := map[string]any{}
	for _, spec := range fields[6:10] {
		option, err := vpnOption(input, spec.source, spec.defaultValue, spec.present)
		if err != nil {
			return nil, err
		}
		advanced[spec.api] = option
	}
	data["advanced"] = advanced
	var schema map[string]any
	encoded, _ := json.Marshal(data)
	var normalized any
	if json.Unmarshal(serviceSVISchema, &schema) != nil || json.Unmarshal(encoded, &normalized) != nil || !validScalarValue(normalized, schema) {
		return nil, errors.New("SVI intent violates provider schema")
	}
	return data, nil
}
