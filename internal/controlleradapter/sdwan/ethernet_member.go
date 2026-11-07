// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"encoding/json"
	"errors"
	"fmt"
)

func ethernetMemberFlag(input map[string]any) (bool, error) {
	value := input["port_channel_member_interface"]
	if value == nil {
		return false, nil
	}
	member, ok := value.(bool)
	if !ok {
		return false, errors.New("port_channel_member_interface must be boolean")
	}
	return member, nil
}

func ethernetMemberData(input map[string]any, schemaBytes []byte, wan bool) (map[string]any, error) {
	allowed := map[string]bool{"name": true, "description": true, "port_channel_member_interface": true}
	fields := []struct {
		source, api  string
		defaultValue any
		present      bool
	}{{"shutdown", "shutdown", true, true}, {"interface_name", "interfaceName", nil, false}, {"interface_description", "description", nil, false}}
	advanced := []struct {
		source, api  string
		defaultValue any
		present      bool
	}{{"duplex", "duplex", nil, false}, {"speed", "speed", nil, false}, {"autonegotiate", "autonegotiate", nil, false}, {"media_type", "mediaType", nil, false}, {"load_interval", "loadInterval", float64(30), true}}
	for _, spec := range append(fields, advanced...) {
		allowed[spec.source] = true
		allowed[spec.source+"_variable"] = true
	}
	for field := range input {
		if !allowed[field] {
			return nil, fmt.Errorf("unsupported Ethernet member-link field %s", field)
		}
	}
	if input["interface_name"] == nil && input["interface_name_variable"] == nil {
		return nil, errors.New("member link requires interface_name")
	}
	data := map[string]any{}
	if err := interfaceFields(input, data, fields); err != nil {
		return nil, err
	}
	data["portChannelMemberInterface"] = map[string]any{"optionType": "global", "value": true}
	advancedData := map[string]any{}
	if err := interfaceFields(input, advancedData, advanced); err != nil {
		return nil, err
	}
	data["advanced"] = advancedData
	if wan {
		data["tunnelInterface"] = map[string]any{"optionType": "default", "value": false}
	}
	var schema map[string]any
	var normalized any
	encoded, _ := json.Marshal(data)
	if json.Unmarshal(schemaBytes, &schema) != nil || json.Unmarshal(encoded, &normalized) != nil {
		return nil, errors.New("invalid member-link schema")
	}
	if !validScalarValue(normalized, schema) {
		for key, value := range normalized.(map[string]any) {
			if !validScalarValue(value, schema["properties"].(map[string]any)[key].(map[string]any)) {
				return nil, fmt.Errorf("Ethernet member-link field %s violates provider schema", key)
			}
		}
		return nil, errors.New("Ethernet member-link intent violates provider schema")
	}
	return data, nil
}
