// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
)

//go:embed definitions/transport-wan-cellular-schema.json
var transportWANCellularSchema []byte

func compileWANCellulars(p FeatureProfile, family, key string) ([]Resource, error) {
	if p.WANVPN == nil {
		return nil, nil
	}
	rows, err := vpnRows(p.WANVPN, "cellular_interfaces")
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 && family != "transport" {
		return nil, errors.New("WAN cellular interfaces require transport profile")
	}
	vpnName := "wan_vpn"
	if p.WANVPN["name"] != nil {
		vpnName, _ = p.WANVPN["name"].(string)
	}
	out := make([]Resource, 0, len(rows))
	for _, row := range rows {
		input, ok := row.(map[string]any)
		if !ok {
			return nil, errors.New("WAN cellular interface must be object")
		}
		name, ok := input["name"].(string)
		if !ok || name == "" || len(name) > 128 {
			return nil, errors.New("WAN cellular interface requires name")
		}
		description := ""
		if input["description"] != nil {
			description, ok = input["description"].(string)
			if !ok {
				return nil, errors.New("WAN cellular description must be string")
			}
		}
		data, err := wanCellularData(input)
		if err != nil {
			return nil, fmt.Errorf("WAN cellular %s: %w", name, err)
		}
		out = append(out, Resource{Key: key + "/wan_vpn/cellular_interfaces/" + name, Name: name, Section: "feature_profiles", Kind: "parcel", Type: "transport_wan_vpn_interface_cellular", Collection: "/dataservice/v1/feature-profile/sdwan/transport", ParentName: p.Name, ParentParcelPath: "wan/vpn", ParentParcelName: vpnName, ParcelPath: "interface/cellular", Body: map[string]any{"name": name, "description": description, "data": data}})
	}
	return out, nil
}

func wanCellularData(input map[string]any) (map[string]any, error) {
	fields := []struct {
		source, api  string
		defaultValue any
		present      bool
	}{{"shutdown", "shutdown", true, true}, {"interface_name", "interfaceName", nil, false}, {"interface_description", "description", nil, false}, {"enable_ipv6", "enableIpv6", true, true}, {"tunnel_interface", "tunnelInterface", false, true}, {"ipv4_nat", "nat", false, true}, {"ipv4_dhcp_helpers", "dhcpHelper", nil, false}, {"service_provider", "serviceProvider", nil, false}, {"bandwidth_upstream", "bandwidthUpstream", nil, false}, {"bandwidth_downstream", "bandwidthDownstream", nil, false}}
	advanced := []struct {
		source, api  string
		defaultValue any
		present      bool
	}{{"ip_mtu", "ipMtu", float64(1500), true}, {"interface_mtu", "intrfMtu", float64(1500), true}, {"tcp_mss", "tcpMss", nil, false}, {"ip_directed_broadcast", "ipDirectedBroadcast", false, true}}
	allowed := map[string]bool{"name": true, "description": true, "arp_entries": true, "ipv4_tracker": true, "ipv4_tracker_group": true}
	for _, spec := range append(fields, advanced...) {
		allowed[spec.source] = true
		allowed[spec.source+"_variable"] = true
	}
	for field := range input {
		if !allowed[field] {
			return nil, fmt.Errorf("unsupported WAN cellular field %s", field)
		}
	}
	if input["interface_name"] == nil && input["interface_name_variable"] == nil {
		return nil, errors.New("WAN cellular requires interface_name")
	}
	data := map[string]any{}
	if err := interfaceFields(input, data, fields); err != nil {
		return nil, err
	}
	data["encapsulation"] = []any{}
	data["natAttributesIpv4"] = map[string]any{"udpTimeout": map[string]any{"optionType": "default", "value": float64(1)}, "tcpTimeout": map[string]any{"optionType": "default", "value": float64(60)}}
	arp, err := managementInterfaceRows(input, "arp_entries", []struct{ source, api string }{{"ip_address", "ipAddress"}, {"mac_address", "macAddress"}})
	if err != nil {
		return nil, err
	}
	data["arp"] = arp
	advancedData := map[string]any{}
	if err := interfaceFields(input, advancedData, advanced); err != nil {
		return nil, err
	}
	data["advanced"] = advancedData
	var schema map[string]any
	var normalized any
	encoded, _ := json.Marshal(data)
	if json.Unmarshal(transportWANCellularSchema, &schema) != nil || json.Unmarshal(encoded, &normalized) != nil {
		return nil, errors.New("invalid WAN cellular schema")
	}
	if !validScalarValue(normalized, schema) {
		for key, value := range normalized.(map[string]any) {
			if !validScalarValue(value, schema["properties"].(map[string]any)[key].(map[string]any)) {
				return nil, fmt.Errorf("WAN cellular field %s violates provider schema", key)
			}
		}
		return nil, errors.New("WAN cellular intent violates provider schema")
	}
	return data, nil
}
