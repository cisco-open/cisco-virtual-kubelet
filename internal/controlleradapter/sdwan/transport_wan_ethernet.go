// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
)

//go:embed definitions/transport-wan-ethernet-schema.json
var transportWANEthernetSchema []byte

func compileWANEthernets(p FeatureProfile, family, key, managerVersion string) ([]Resource, error) {
	if p.WANVPN == nil {
		return nil, nil
	}
	rows, err := vpnRows(p.WANVPN, "ethernet_interfaces")
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 && family != "transport" {
		return nil, errors.New("WAN Ethernet interfaces require transport profile")
	}
	modern, err := managementInterfaceVersion(managerVersion)
	if err != nil {
		return nil, err
	}
	vpnName := "wan_vpn"
	if p.WANVPN["name"] != nil {
		vpnName, _ = p.WANVPN["name"].(string)
	}
	out := make([]Resource, 0, len(rows))
	for _, row := range rows {
		input, ok := row.(map[string]any)
		if !ok {
			return nil, errors.New("WAN Ethernet interface must be object")
		}
		name, ok := input["name"].(string)
		if !ok || name == "" || len(name) > 128 {
			return nil, errors.New("WAN Ethernet interface requires name")
		}
		description := ""
		if input["description"] != nil {
			description, ok = input["description"].(string)
			if !ok {
				return nil, errors.New("WAN Ethernet description must be string")
			}
		}
		data, err := wanEthernetData(input, modern)
		if err != nil {
			return nil, fmt.Errorf("WAN Ethernet %s: %w", name, err)
		}
		out = append(out, Resource{Key: key + "/wan_vpn/ethernet_interfaces/" + name, Name: name, Section: "feature_profiles", Kind: "parcel", Type: "transport_wan_vpn_interface_ethernet", Collection: "/dataservice/v1/feature-profile/sdwan/transport", ParentName: p.Name, ParentParcelPath: "wan/vpn", ParentParcelName: vpnName, ParcelPath: "interface/ethernet", Body: map[string]any{"name": name, "description": description, "data": data}})
	}
	return out, nil
}

func wanEthernetData(input map[string]any, modern bool) (map[string]any, error) {
	fields := []struct {
		source, api  string
		defaultValue any
		present      bool
	}{{"shutdown", "shutdown", true, true}, {"interface_name", "interfaceName", nil, false}, {"interface_description", "description", nil, false}, {"tunnel_interface", "tunnelInterface", false, true}}
	advanced := []struct {
		source, api  string
		defaultValue any
		present      bool
	}{{"duplex", "duplex", nil, false}, {"mac_address", "macAddress", nil, false}, {"ip_mtu", "ipMtu", float64(1500), true}, {"interface_mtu", "intrfMtu", float64(1500), true}, {"tcp_mss", "tcpMss", nil, false}, {"speed", "speed", nil, false}, {"arp_timeout", "arpTimeout", float64(1200), true}, {"autonegotiate", "autonegotiate", nil, false}, {"media_type", "mediaType", nil, false}, {"load_interval", "loadInterval", float64(30), true}, {"ip_directed_broadcast", "ipDirectedBroadcast", false, true}}
	allowed := map[string]bool{"name": true, "description": true, "ipv4_tracker": true, "ipv4_tracker_group": true, "ipv6_tracker": true, "ipv6_tracker_group": true, "ipv4_address_type": true, "ipv4_address_type_variable": true, "ipv4_address": true, "ipv4_address_variable": true, "ipv4_subnet_mask": true, "ipv4_subnet_mask_variable": true, "ipv4_dhcp_distance": true, "ipv4_dhcp_distance_variable": true, "ipv4_secondary_addresses": true, "ipv6_address_type": true, "ipv6_address_type_variable": true, "ipv6_address": true, "ipv6_address_variable": true, "arp_entries": true, "ipv4_dhcp_helpers": true, "ipv4_dhcp_helpers_variable": true}
	for _, spec := range append(fields, advanced...) {
		allowed[spec.source] = true
		allowed[spec.source+"_variable"] = true
	}
	for field := range input {
		if !allowed[field] {
			return nil, fmt.Errorf("unsupported WAN Ethernet field %s", field)
		}
	}
	if input["interface_name"] == nil && input["interface_name_variable"] == nil {
		return nil, errors.New("WAN Ethernet requires interface_name")
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
	data["encapsulation"] = []any{}
	data["natIpv6"] = map[string]any{"optionType": "default", "value": false}
	data["natAttributesIpv4"] = map[string]any{"natType": map[string]any{"optionType": "global", "value": "pool"}, "udpTimeout": map[string]any{"optionType": "default", "value": float64(1)}, "tcpTimeout": map[string]any{"optionType": "default", "value": float64(60)}, "newStaticNat": []any{}}
	data["trustsec"] = map[string]any{"enableSGTPropogation": map[string]any{"optionType": "default", "value": false}, "propagate": map[string]any{"optionType": "default", "value": true}, "securityGroupTag": map[string]any{"optionType": "default"}, "enableEnforcedPropogation": map[string]any{"optionType": "default"}, "enforcedSecurityGroupTag": map[string]any{"optionType": "default"}}
	advancedData := map[string]any{}
	if err := interfaceFields(input, advancedData, advanced); err != nil {
		return nil, err
	}
	data["advanced"] = advancedData
	var schema map[string]any
	var normalized any
	encoded, _ := json.Marshal(data)
	if json.Unmarshal(transportWANEthernetSchema, &schema) != nil || json.Unmarshal(encoded, &normalized) != nil {
		return nil, errors.New("invalid WAN Ethernet schema")
	}
	if !validScalarValue(normalized, schema) {
		for key, value := range normalized.(map[string]any) {
			if !validScalarValue(value, schema["properties"].(map[string]any)[key].(map[string]any)) {
				return nil, fmt.Errorf("WAN Ethernet field %s violates provider schema", key)
			}
		}
		for index, clause := range schema["allOf"].([]any) {
			if !validScalarValue(normalized, clause.(map[string]any)) {
				return nil, fmt.Errorf("WAN Ethernet schema allOf %d failed at %s", index, wanSchemaFailure(normalized, clause.(map[string]any), "root"))
			}
		}
		return nil, errors.New("WAN Ethernet intent violates provider schema")
	}
	return data, nil
}

func wanSchemaFailure(value any, schema map[string]any, path string) string {
	if branches, ok := schema["allOf"].([]any); ok {
		for i, branch := range branches {
			b := branch.(map[string]any)
			if !validScalarValue(value, b) {
				return wanSchemaFailure(value, b, fmt.Sprintf("%s/allOf[%d]", path, i))
			}
		}
	}
	if condition, ok := schema["if"].(map[string]any); ok {
		branch := "else"
		if validScalarValue(value, condition) {
			branch = "then"
		}
		if b, ok := schema[branch].(map[string]any); ok && !validScalarValue(value, b) {
			return wanSchemaFailure(value, b, path+"/"+branch)
		}
	}
	if branches, ok := schema["oneOf"].([]any); ok {
		return fmt.Sprintf("%s/oneOf(%d alternatives)", path, len(branches))
	}
	if required, ok := schema["required"].([]any); ok {
		object, _ := value.(map[string]any)
		for _, name := range required {
			if _, ok := object[name.(string)]; !ok {
				return path + "/required:" + name.(string)
			}
		}
	}
	return path
}
