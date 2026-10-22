// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
)

//go:embed definitions/service-ipsec-schema.json
var serviceIPSecSchema []byte

//go:embed definitions/transport-ipsec-schema.json
var transportIPSecSchema []byte

func compileIPSecInterfaces(p FeatureProfile, family, key string) ([]Resource, error) {
	out := []Resource{}
	for _, spec := range []struct {
		parent                                      map[string]any
		parentPath, field, collection, resourceType string
		schema                                      []byte
	}{
		{nil, "lan/vpn", "ipsec_interfaces", "service", "service_lan_vpn_interface_ipsec", serviceIPSecSchema},
		{p.WANVPN, "wan/vpn", "ipsec_interfaces", "transport", "transport_wan_vpn_interface_ipsec", transportIPSecSchema},
	} {
		parents := []map[string]any{}
		if spec.collection == "service" {
			parents = p.LANVPNs
		} else if spec.parent != nil {
			parents = []map[string]any{spec.parent}
		}
		for _, parent := range parents {
			rows, err := vpnRows(parent, spec.field)
			if err != nil {
				return nil, err
			}
			if len(rows) > 0 && family != spec.collection {
				return nil, fmt.Errorf("IPsec interfaces require %s profile", spec.collection)
			}
			parentName, _ := parent["name"].(string)
			if parentName == "" && spec.collection == "transport" {
				parentName = "wan_vpn"
			}
			for _, row := range rows {
				input, ok := row.(map[string]any)
				if !ok {
					return nil, errors.New("IPsec interface must be object")
				}
				name, ok := input["name"].(string)
				if !ok || name == "" || len(name) > 128 {
					return nil, errors.New("IPsec interface requires name")
				}
				description := ""
				if input["description"] != nil {
					description, ok = input["description"].(string)
					if !ok {
						return nil, errors.New("IPsec description must be string")
					}
				}
				data, err := ipsecData(input, spec.schema, spec.collection == "transport")
				if err != nil {
					return nil, fmt.Errorf("IPsec %s: %w", name, err)
				}
				keyPath := "/lan_vpns/" + parentName
				if spec.collection == "transport" {
					keyPath = "/wan_vpn"
				}
				out = append(out, Resource{Key: key + keyPath + "/ipsec_interfaces/" + name, Name: name, Section: "feature_profiles", Kind: "parcel", Type: spec.resourceType, Collection: "/dataservice/v1/feature-profile/sdwan/" + spec.collection, ParentName: p.Name, ParentParcelPath: spec.parentPath, ParentParcelName: parentName, ParcelPath: "interface/ipsec", Body: map[string]any{"name": name, "description": description, "data": data}})
			}
		}
	}
	return out, nil
}

func ipsecData(input map[string]any, schemaBytes []byte, allowTracker bool) (map[string]any, error) {
	fields := []struct {
		source, api  string
		defaultValue any
		present      bool
	}{
		{"interface_name", "ifName", nil, false}, {"shutdown", "shutdown", false, true}, {"interface_description", "description", nil, false},
		{"ike_preshared_key", "preSharedSecret", nil, false}, {"ike_version", "ikeVersion", float64(1), true},
		{"ipv4_mtu", "mtu", float64(1500), true}, {"ipv4_tcp_mss", "tcpMssAdjust", nil, false},
		{"application_tunnel_type", "application", "none", true}, {"clear_dont_fragment", "clearDontFragment", false, true},
		{"dpd_interval", "dpdInterval", float64(10), true}, {"dpd_retries", "dpdRetries", float64(3), true},
		{"ike_rekey_interval", "ikeRekeyInterval", float64(14400), true}, {"ike_cipher_suite", "ikeCiphersuite", "aes256-cbc-sha1", true},
		{"ike_diffie_hellman_group", "ikeGroup", "16", true}, {"ike_local_endpoint_id", "ikeLocalId", nil, false},
		{"ike_remote_endpoint_id", "ikeRemoteId", nil, false}, {"ipsec_rekey_interval", "ipsecRekeyInterval", float64(3600), true},
		{"ipsec_replay_window", "ipsecReplayWindow", float64(512), true}, {"ipsec_cipher_suite", "ipsecCiphersuite", "aes256-gcm", true},
		{"perfect_forward_secrecy", "perfectForwardSecrecy", "group-16", true},
	}
	allowed := map[string]bool{"name": true, "description": true, "ipv4_address": true, "ipv4_address_variable": true, "ipv4_subnet_mask": true, "ipv4_subnet_mask_variable": true, "tunnel_source_ipv4_address": true, "tunnel_source_ipv4_address_variable": true, "tunnel_destination_ipv4_address": true, "tunnel_destination_ipv4_address_variable": true}
	if allowTracker {
		allowed["ipv4_tracker"] = true
	}
	for _, spec := range fields {
		allowed[spec.source] = true
		allowed[spec.source+"_variable"] = true
	}
	for field := range input {
		if !allowed[field] {
			return nil, fmt.Errorf("unsupported IPsec field %s", field)
		}
	}
	for _, field := range []string{"interface_name", "ipv4_address", "ipv4_subnet_mask", "tunnel_source_ipv4_address", "tunnel_destination_ipv4_address", "ike_preshared_key"} {
		if input[field] == nil && input[field+"_variable"] == nil {
			return nil, fmt.Errorf("IPsec requires %s", field)
		}
	}
	data := map[string]any{"tunnelMode": map[string]any{"optionType": "default", "value": "ipv4"}}
	for _, spec := range fields {
		value, err := vpnOption(input, spec.source, spec.defaultValue, spec.present)
		if err != nil {
			return nil, err
		}
		if spec.api == "application" && input[spec.source] == nil && input[spec.source+"_variable"] == nil {
			value = map[string]any{"optionType": "global", "value": "none"}
		}
		data[spec.api] = value
	}
	address := map[string]any{}
	for _, spec := range []struct{ source, api string }{{"ipv4_address", "address"}, {"ipv4_subnet_mask", "mask"}} {
		value, err := vpnOption(input, spec.source, nil, false)
		if err != nil {
			return nil, err
		}
		address[spec.api] = value
	}
	data["address"] = address
	for _, spec := range []struct{ source, api string }{{"tunnel_source_ipv4_address", "tunnelSource"}, {"tunnel_destination_ipv4_address", "tunnelDestination"}} {
		address, err := vpnOption(input, spec.source, nil, false)
		if err != nil {
			return nil, err
		}
		data[spec.api] = map[string]any{"address": address, "mask": map[string]any{"optionType": "global", "value": "0.0.0.0"}}
	}
	var schema map[string]any
	var normalized any
	encoded, _ := json.Marshal(data)
	if json.Unmarshal(schemaBytes, &schema) != nil || json.Unmarshal(encoded, &normalized) != nil {
		return nil, errors.New("invalid IPsec schema")
	}
	if !validScalarValue(normalized, schema) {
		for key, value := range normalized.(map[string]any) {
			if !validScalarValue(value, schema["properties"].(map[string]any)[key].(map[string]any)) {
				return nil, fmt.Errorf("IPsec field %s violates provider schema", key)
			}
		}
		return nil, errors.New("IPsec intent violates provider schema")
	}
	return data, nil
}
