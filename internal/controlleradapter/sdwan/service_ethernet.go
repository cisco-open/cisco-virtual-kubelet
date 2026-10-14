// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
)

//go:embed definitions/service-ethernet-schema.json
var serviceEthernetSchema []byte

func compileServiceEthernets(p FeatureProfile, family, key, managerVersion string) ([]Resource, error) {
	out := []Resource{}
	modern, err := managementInterfaceVersion(managerVersion)
	if err != nil {
		return nil, err
	}
	for _, vpn := range p.LANVPNs {
		rows, err := vpnRows(vpn, "ethernet_interfaces")
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 && family != "service" {
			return nil, errors.New("service Ethernet interfaces require service profile")
		}
		vpnName, _ := vpn["name"].(string)
		for _, row := range rows {
			input, ok := row.(map[string]any)
			if !ok {
				return nil, errors.New("service Ethernet interface must be object")
			}
			name, ok := input["name"].(string)
			if !ok || name == "" || len(name) > 128 {
				return nil, errors.New("service Ethernet interface requires name")
			}
			description := ""
			if input["description"] != nil {
				description, ok = input["description"].(string)
				if !ok {
					return nil, errors.New("service Ethernet description must be string")
				}
			}
			data, err := serviceEthernetData(input, modern)
			if err != nil {
				return nil, fmt.Errorf("service Ethernet %s: %w", name, err)
			}
			out = append(out, Resource{Key: key + "/lan_vpns/" + vpnName + "/ethernet_interfaces/" + name, Name: name, Section: "feature_profiles", Kind: "parcel", Type: "service_lan_vpn_interface_ethernet", Collection: "/dataservice/v1/feature-profile/sdwan/service", ParentName: p.Name, ParentParcelPath: "lan/vpn", ParentParcelName: vpnName, ParcelPath: "interface/ethernet", Body: map[string]any{"name": name, "description": description, "data": data}})
		}
	}
	return out, nil
}

func serviceEthernetData(input map[string]any, modern bool) (map[string]any, error) {
	fields := []struct {
		source, api  string
		defaultValue any
		present      bool
	}{
		{"shutdown", "shutdown", true, true}, {"interface_name", "interfaceName", nil, false}, {"interface_description", "description", nil, false},
	}
	advanced := []struct {
		source, api  string
		defaultValue any
		present      bool
	}{
		{"duplex", "duplex", nil, false}, {"mac_address", "macAddress", nil, false}, {"ip_mtu", "ipMtu", float64(1500), true}, {"interface_mtu", "intrfMtu", float64(1500), true}, {"tcp_mss", "tcpMss", nil, false}, {"speed", "speed", nil, false}, {"arp_timeout", "arpTimeout", float64(1200), true}, {"autonegotiate", "autonegotiate", nil, false}, {"media_type", "mediaType", nil, false}, {"load_interval", "loadInterval", float64(30), true}, {"icmp_redirect_disable", "icmpRedirectDisable", true, true}, {"ip_directed_broadcast", "ipDirectedBroadcast", false, true},
	}
	allowed := map[string]bool{"name": true, "description": true, "dhcp_server": true, "ipv4_tracker": true, "ipv4_tracker_group": true, "ipv4_address_type": true, "ipv4_address_type_variable": true, "ipv4_address": true, "ipv4_address_variable": true, "ipv4_subnet_mask": true, "ipv4_subnet_mask_variable": true, "ipv4_dhcp_distance": true, "ipv4_dhcp_distance_variable": true, "ipv4_secondary_addresses": true, "ipv6_address_type": true, "ipv6_address_type_variable": true, "ipv6_address": true, "ipv6_address_variable": true, "arp_entries": true, "ipv4_dhcp_helpers": true, "ipv4_dhcp_helpers_variable": true}
	for _, spec := range append(fields, advanced...) {
		allowed[spec.source] = true
		allowed[spec.source+"_variable"] = true
	}
	for field := range input {
		if !allowed[field] {
			return nil, fmt.Errorf("unsupported service Ethernet field %s", field)
		}
	}
	if input["interface_name"] == nil && input["interface_name_variable"] == nil {
		return nil, errors.New("interface_name is required")
	}
	data := map[string]any{}
	if err := interfaceFields(input, data, fields); err != nil {
		return nil, err
	}
	ipv4, err := managementIPv4Address(input, modern)
	if err != nil {
		return nil, err
	}
	if modern && input["ipv4_address_type"] == nil && input["ipv4_address_type_variable"] == nil {
		ipv4["either"].(map[string]any)["addressType"] = map[string]any{"optionType": "global", "value": "static"}
	}
	data["intfIpAddress"] = ipv4
	ipv6, err := managementIPv6Address(input, modern)
	if err != nil {
		return nil, err
	}
	if ipv6 != nil {
		data["intfIpV6Address"] = ipv6
	}
	dhcp, err := vpnOption(input, "ipv4_dhcp_helpers", nil, false)
	if err != nil {
		return nil, err
	}
	data["dhcpHelper"] = dhcp
	arp, err := managementInterfaceRows(input, "arp_entries", []struct{ source, api string }{{"ip_address", "ipAddress"}, {"mac_address", "macAddress"}})
	if err != nil {
		return nil, err
	}
	data["arp"] = arp
	data["nat"] = map[string]any{"optionType": "default", "value": false}
	data["natIpv6"] = map[string]any{"optionType": "default", "value": false}
	data["natAttributesIpv4"] = map[string]any{"natType": map[string]any{"optionType": "global", "value": "pool"}, "udpTimeout": map[string]any{"optionType": "default", "value": float64(1)}, "tcpTimeout": map[string]any{"optionType": "default", "value": float64(60)}, "newStaticNat": []any{}}
	data["vrrp"] = []any{}
	data["vrrpIpv6"] = []any{}
	data["trustsec"] = map[string]any{"enableSGTPropogation": map[string]any{"optionType": "default", "value": false}, "propogate": map[string]any{"optionType": "default", "value": true}, "securityGroupTag": map[string]any{"optionType": "default"}, "enableEnforcedPropogation": map[string]any{"optionType": "default"}, "enforcedSecurityGroupTag": map[string]any{"optionType": "default"}}
	advancedData := map[string]any{}
	if err := interfaceFields(input, advancedData, advanced); err != nil {
		return nil, err
	}
	data["advanced"] = advancedData
	var schema map[string]any
	encoded, _ := json.Marshal(data)
	var normalized any
	if json.Unmarshal(serviceEthernetSchema, &schema) != nil || json.Unmarshal(encoded, &normalized) != nil {
		return nil, errors.New("invalid service Ethernet schema")
	}
	if !validScalarValue(normalized, schema) {
		for key, value := range normalized.(map[string]any) {
			if !validScalarValue(value, schema["properties"].(map[string]any)[key].(map[string]any)) {
				return nil, fmt.Errorf("service Ethernet field %s violates provider schema", key)
			}
		}
		return nil, errors.New("service Ethernet intent violates provider schema")
	}
	return data, nil
}
