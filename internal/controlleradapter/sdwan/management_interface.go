// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

//go:embed definitions/transport-management-interface-schema.json
var managementInterfaceSchema []byte

func managementInterfaceVersion(managerVersion string) (bool, error) {
	if managerVersion == "" {
		return true, nil
	}
	parts := strings.Split(managerVersion, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return false, errors.New("invalid manager_version")
	}
	major, e1 := strconv.Atoi(parts[0])
	minor, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil || major < 0 || minor < 0 {
		return false, errors.New("invalid manager_version")
	}
	return major > 20 || major == 20 && minor >= 18, nil
}

func interfaceOption(input map[string]any, field string, defaultValue any, present bool) (map[string]any, error) {
	return vpnOption(input, field, defaultValue, present)
}

func interfaceFields(input map[string]any, target map[string]any, specs []struct {
	source, api  string
	defaultValue any
	present      bool
}) error {
	for _, spec := range specs {
		option, err := interfaceOption(input, spec.source, spec.defaultValue, spec.present)
		if err != nil {
			return err
		}
		target[spec.api] = option
	}
	return nil
}

func compileManagementInterfaces(p FeatureProfile, collection, key, managerVersion string) ([]Resource, error) {
	if p.ManagementVPN == nil || p.ManagementVPN["ethernet_interfaces"] == nil {
		return nil, nil
	}
	if collection != "transport" {
		return nil, errors.New("management interfaces require a transport profile")
	}
	modern, err := managementInterfaceVersion(managerVersion)
	if err != nil {
		return nil, err
	}
	rows, err := vpnRows(p.ManagementVPN, "ethernet_interfaces")
	if err != nil {
		return nil, err
	}
	parentName := "management_vpn"
	if p.ManagementVPN["name"] != nil {
		parentName, _ = p.ManagementVPN["name"].(string)
	}
	out := make([]Resource, 0, len(rows))
	for _, row := range rows {
		input, ok := row.(map[string]any)
		if !ok {
			return nil, errors.New("management Ethernet interface must be an object")
		}
		name, ok := input["name"].(string)
		if !ok || name == "" || len(name) > 128 {
			return nil, errors.New("management Ethernet interface requires name")
		}
		description := ""
		if input["description"] != nil {
			var ok bool
			description, ok = input["description"].(string)
			if !ok {
				return nil, errors.New("management Ethernet interface description must be a string")
			}
		}
		data, err := managementInterfaceData(input, modern)
		if err != nil {
			return nil, fmt.Errorf("management Ethernet %s: %w", name, err)
		}
		out = append(out, Resource{Key: key + "/management_vpn/ethernet_interfaces/" + name, Name: name, Section: "feature_profiles", Kind: "parcel", Type: "transport_management_vpn_interface_ethernet", Collection: "/dataservice/v1/feature-profile/sdwan/transport", ParentName: p.Name, ParentParcelPath: "management/vpn", ParentParcelName: parentName, ParcelPath: "interface/ethernet", Body: map[string]any{"name": name, "description": description, "data": data}})
	}
	return out, nil
}

func managementInterfaceData(input map[string]any, modern bool) (map[string]any, error) {
	allowed := map[string]bool{"name": true, "description": true, "arp_entries": true, "ipv4_secondary_addresses": true, "ipv4_address_type": true, "ipv4_address_type_variable": true, "ipv4_address": true, "ipv4_address_variable": true, "ipv4_subnet_mask": true, "ipv4_subnet_mask_variable": true, "ipv4_dhcp_distance": true, "ipv4_dhcp_distance_variable": true, "ipv6_address_type": true, "ipv6_address_type_variable": true, "ipv6_address": true, "ipv6_address_variable": true}
	fields := []struct {
		source, api  string
		defaultValue any
		present      bool
	}{
		{"shutdown", "shutdown", true, true}, {"interface_name", "interfaceName", nil, false}, {"interface_description", "description", nil, false},
		{"ipv4_dhcp_helpers", "dhcpHelper", nil, false}, {"iperf_server", "iperfServer", nil, false}, {"auto_detect_bandwidth", "autoDetectBandwidth", false, true},
	}
	advanced := []struct {
		source, api  string
		defaultValue any
		present      bool
	}{
		{"duplex", "duplex", nil, false}, {"mac_address", "macAddress", nil, false}, {"ip_mtu", "ipMtu", float64(1500), true}, {"interface_mtu", "intrfMtu", float64(1500), true}, {"tcp_mss", "tcpMss", nil, false}, {"speed", "speed", nil, false}, {"arp_timeout", "arpTimeout", float64(1200), true}, {"autonegotiate", "autonegotiate", nil, false}, {"media_type", "mediaType", nil, false}, {"load_interval", "loadInterval", float64(30), true}, {"icmp_redirect_disable", "icmpRedirectDisable", true, true}, {"ip_directed_broadcast", "ipDirectedBroadcast", false, true},
	}
	for _, spec := range append(fields, advanced...) {
		allowed[spec.source] = true
		allowed[spec.source+"_variable"] = true
	}
	for field := range input {
		if !allowed[field] {
			return nil, fmt.Errorf("unsupported interface field %s", field)
		}
	}
	data := map[string]any{}
	if err := interfaceFields(input, data, fields); err != nil {
		return nil, err
	}
	if input["interface_name"] == nil && input["interface_name_variable"] == nil {
		return nil, errors.New("interface_name is required")
	}
	advancedData := map[string]any{}
	if err := interfaceFields(input, advancedData, advanced); err != nil {
		return nil, err
	}
	data["advanced"] = advancedData
	ipv4, err := managementIPv4Address(input, modern)
	if err != nil {
		return nil, err
	}
	data["intfIpAddress"] = ipv4
	ipv6, err := managementIPv6Address(input, modern)
	if err != nil {
		return nil, err
	}
	if ipv6 != nil {
		data["intfIpV6Address"] = ipv6
	}
	arp, err := managementInterfaceRows(input, "arp_entries", []struct{ source, api string }{{"ip_address", "ipAddress"}, {"mac_address", "macAddress"}})
	if err != nil {
		return nil, err
	}
	data["arp"] = arp
	var schema map[string]any
	encoded, _ := json.Marshal(data)
	var normalized any
	if json.Unmarshal(managementInterfaceSchema, &schema) != nil || json.Unmarshal(encoded, &normalized) != nil || !validScalarValue(normalized, schema) {
		return nil, errors.New("management Ethernet intent violates provider schema")
	}
	return data, nil
}

func managementInterfaceRows(input map[string]any, field string, specs []struct{ source, api string }) ([]any, error) {
	rows, err := vpnRows(input, field)
	if err != nil {
		return nil, err
	}
	out := []any{}
	for _, row := range rows {
		entry, ok := row.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s entry must be an object", field)
		}
		body := map[string]any{}
		allowed := map[string]bool{}
		for _, spec := range specs {
			allowed[spec.source] = true
			allowed[spec.source+"_variable"] = true
			option, err := vpnOptional(entry, spec.source)
			if err != nil {
				return nil, err
			}
			if option != nil {
				body[spec.api] = option
			}
		}
		for key := range entry {
			if !allowed[key] {
				return nil, fmt.Errorf("unsupported %s field %s", field, key)
			}
		}
		out = append(out, body)
	}
	return out, nil
}

func managementIPv4Address(input map[string]any, modern bool) (map[string]any, error) {
	typeName := "static"
	if input["ipv4_address_type"] != nil {
		var ok bool
		typeName, ok = input["ipv4_address_type"].(string)
		if !ok {
			return nil, errors.New("invalid IPv4 address type")
		}
	}
	if typeName != "static" && typeName != "dynamic" {
		return nil, errors.New("invalid IPv4 address type")
	}
	variable := input["ipv4_address_type_variable"] != nil
	if variable && input["ipv4_address_type"] != nil {
		return nil, errors.New("IPv4 address type and variable conflict")
	}
	address := map[string]any{}
	branch := address
	if modern {
		branch = map[string]any{}
		address["either"] = branch
		option, err := vpnOption(input, "ipv4_address_type", typeName, true)
		if err != nil {
			return nil, err
		}
		branch["addressType"] = option
	}
	if typeName == "dynamic" || variable {
		option, err := vpnOption(input, "ipv4_dhcp_distance", float64(1), true)
		if err != nil {
			return nil, err
		}
		branch["dynamic"] = map[string]any{"dynamicDhcpDistance": option}
	} else if input["ipv4_dhcp_distance"] != nil || input["ipv4_dhcp_distance_variable"] != nil {
		return nil, errors.New("DHCP distance requires dynamic address")
	}
	if typeName == "static" || variable {
		primary := map[string]any{}
		for _, spec := range []struct{ source, api string }{{"ipv4_address", "ipAddress"}, {"ipv4_subnet_mask", "subnetMask"}} {
			option, err := vpnOption(input, spec.source, nil, false)
			if err != nil {
				return nil, err
			}
			primary[spec.api] = option
		}
		secondary, err := managementInterfaceRows(input, "ipv4_secondary_addresses", []struct{ source, api string }{{"address", "ipAddress"}, {"subnet_mask", "subnetMask"}})
		if err != nil {
			return nil, err
		}
		branch["static"] = map[string]any{"staticIpV4AddressPrimary": primary, "staticIpV4AddressSecondary": secondary}
	} else if input["ipv4_address"] != nil || input["ipv4_subnet_mask"] != nil || input["ipv4_secondary_addresses"] != nil {
		return nil, errors.New("static IPv4 fields require static address")
	}
	return address, nil
}

func managementIPv6Address(input map[string]any, modern bool) (map[string]any, error) {
	if input["ipv6_address_type"] == nil && input["ipv6_address_type_variable"] == nil {
		if input["ipv6_address"] != nil || input["ipv6_address_variable"] != nil {
			return nil, errors.New("IPv6 address requires address type")
		}
		return nil, nil
	}
	typeName, _ := input["ipv6_address_type"].(string)
	if typeName != "" && typeName != "static" && typeName != "dynamic" {
		return nil, errors.New("invalid IPv6 address type")
	}
	variable := input["ipv6_address_type_variable"] != nil
	if variable && typeName != "" {
		return nil, errors.New("IPv6 address type and variable conflict")
	}
	address := map[string]any{}
	branch := address
	if modern {
		branch = map[string]any{}
		address["either"] = branch
		option, err := vpnOptional(input, "ipv6_address_type")
		if err != nil {
			return nil, err
		}
		branch["addressType"] = option
	}
	if typeName == "dynamic" || variable {
		branch["dynamic"] = map[string]any{"dhcpClient": aclOption(true)}
	}
	if typeName == "static" || variable {
		option, err := vpnOption(input, "ipv6_address", nil, false)
		if err != nil {
			return nil, err
		}
		branch["static"] = map[string]any{"primaryIpV6Address": map[string]any{"address": option}}
	}
	return address, nil
}
