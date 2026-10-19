// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/netip"
	"sort"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

var aaaGroups = []struct{ model, api string }{{"network_aaa", "aaaNetwork"}, {"client_and_endpoint_aaa", "aaaClient"}}
var aaaFields = []struct{ model, api string }{{"server_type", "serverType"}, {"protocol", "protocol"}, {"primary_ip", "primaryServerIp"}, {"secondary_ip", "secondaryServerIp"}, {"pan", "pan"}}

func canonicalAAAAddress(value string) (string, error) {
	ip, err := netip.ParseAddr(value)
	if err != nil || ip.Zone() != "" {
		return "", errors.New("invalid AAA address")
	}
	return ip.String(), nil
}
func validateAAAFields(fields map[string]string) error {
	if fields["serverType"] != "AAA" && fields["serverType"] != "ISE" {
		return errors.New("invalid AAA server type")
	}
	if fields["protocol"] != "RADIUS" && fields["protocol"] != "TACACS" {
		return errors.New("invalid AAA protocol")
	}
	for _, key := range []string{"primaryServerIp", "secondaryServerIp", "pan"} {
		value, present := fields[key]
		required := key == "primaryServerIp" || (key == "pan" && fields["serverType"] == "ISE")
		if !present && !required {
			continue
		}
		normalized, err := canonicalAAAAddress(value)
		if err != nil {
			return err
		}
		fields[key] = normalized
	}
	if fields["serverType"] == "AAA" {
		if _, ok := fields["pan"]; ok {
			return errors.New("PAN is supported only for ISE")
		}
	}
	return nil
}

// Metadata-only intent: reject shared_secret and its version rather than
// treating an unreadable or masked credential as verified configuration.
func parseAAAProfile(p map[string]any) ([]networkSetting, error) {
	for key := range p {
		if key != "name" && key != "network_aaa" && key != "client_and_endpoint_aaa" {
			return nil, errors.New("unsupported AAA profile field")
		}
	}
	groups := map[string]map[string]string{}
	for _, group := range aaaGroups {
		raw, present := p[group.model]
		if !present {
			continue
		}
		object, ok := raw.(map[string]any)
		if !ok || len(object) == 0 {
			return nil, errors.New("AAA group must contain explicit metadata")
		}
		fields := map[string]string{}
		for key, value := range object {
			mapped := ""
			for _, field := range aaaFields {
				if field.model == key {
					mapped = field.api
					break
				}
			}
			if mapped == "" {
				return nil, errors.New("unsupported AAA field; credential comparison is not implemented")
			}
			text, ok := value.(string)
			if !ok {
				return nil, errors.New("AAA field must be an explicit string")
			}
			fields[mapped] = text
		}
		if err := validateAAAFields(fields); err != nil {
			return nil, err
		}
		groups[group.api] = fields
	}
	if len(groups) == 0 {
		return nil, errors.New("AAA profile must select at least one group")
	}
	return []networkSetting{{kind: "aaa", aaa: groups}}, nil
}

func parseAAAObservation(response map[string]json.RawMessage) (map[string]map[string]string, error) {
	groups := map[string]map[string]string{}
	for _, group := range aaaGroups {
		raw, present := response[group.api]
		if !present {
			return nil, errInvalidResponse
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			groups[group.api] = nil
			continue
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil || object == nil {
			return nil, errInvalidResponse
		}
		fields := map[string]string{}
		groups[group.api] = fields
		if len(object) == 0 {
			continue
		}
		for _, key := range []string{"inheritedSiteId", "inheritedSiteName"} {
			if raw, ok := object[key]; ok {
				var identity string
				if json.Unmarshal(raw, &identity) != nil || identity != "" {
					return nil, errInvalidResponse
				}
			}
		}
		for _, field := range aaaFields {
			raw, present := object[field.api]
			required := field.api == "serverType" || field.api == "protocol" || field.api == "primaryServerIp"
			if !present && !required {
				continue
			}
			var value string
			if !readSettingString(object, field.api, &value) {
				// Optional unset API values are not proof of a requested value.
				if !required && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
					continue
				}
				return nil, errInvalidResponse
			}
			if value == "" && !required {
				continue
			}
			fields[field.api] = value
		}
		if err := validateAAAFields(fields); err != nil {
			return nil, errInvalidResponse
		}
		// sharedSecret is deliberately never decoded into the observation.
	}
	return groups, nil
}

func aaaDrift(path string, want, got map[string]map[string]string) []configapi.NetworkControllerDriftEntry {
	out := []configapi.NetworkControllerDriftEntry{}
	add := func(field string) {
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "network_settings", Resource: path, Path: "/aaa_servers/" + field, Change: configapi.NetworkControllerDriftChangeUpdate})
	}
	for _, group := range aaaGroups {
		desired, selected := want[group.api]
		if !selected {
			continue
		}
		observed := got[group.api]
		if len(observed) == 0 {
			add(group.model)
			continue
		}
		keys := make([]string, 0, len(desired))
		for key := range desired {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if observed[key] != desired[key] {
				for _, field := range aaaFields {
					if field.api == key {
						add(group.model + "/" + field.model)
						break
					}
				}
			}
		}
	}
	return out
}
