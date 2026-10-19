// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

var telemetryGroups = []string{"wiredDataCollection", "wirelessTelemetry", "snmpTraps", "syslogs", "applicationVisibility"}

// Null/omitted values are unselected; explicit false must remain actionable.
func parseTelemetryProfile(p map[string]any) ([]networkSetting, error) {
	allowed := map[string]bool{"name": true, "wired_data_collection": true, "wireless_telemetry": true, "catalyst_center_as_snmp_server": true, "snmp_servers": true, "catalyst_center_as_syslog_server": true, "syslog_servers": true, "catalyst_center_as_network_collector": true, "enable_netflow_collector_on_devices": true, "netflow_collector_ip_address": true, "netflow_collector_port": true}
	for key := range p {
		if !allowed[key] {
			return nil, errors.New("unsupported telemetry profile field")
		}
	}
	groups := map[string]map[string]any{}
	for _, g := range telemetryGroups {
		groups[g] = map[string]any{}
	}
	for _, m := range []struct{ model, group, field string }{
		{"wired_data_collection", "wiredDataCollection", "enableWiredDataCollection"},
		{"wireless_telemetry", "wirelessTelemetry", "enableWirelessTelemetry"},
		{"catalyst_center_as_snmp_server", "snmpTraps", "useBuiltinTrapServer"},
		{"catalyst_center_as_syslog_server", "syslogs", "useBuiltinSyslogServer"},
		{"enable_netflow_collector_on_devices", "applicationVisibility", "enableOnWiredAccessDevices"},
	} {
		value, present := p[m.model]
		if !present || value == nil {
			continue
		}
		v, ok := value.(bool)
		if !ok {
			return nil, errors.New("invalid telemetry boolean")
		}
		groups[m.group][m.field] = v
	}
	for _, m := range []struct{ model, group, field string }{{"snmp_servers", "snmpTraps", "externalTrapServers"}, {"syslog_servers", "syslogs", "externalSyslogServers"}} {
		value, present := p[m.model]
		if !present || value == nil {
			continue
		}
		v, err := parseServerList(value, "telemetry")
		if err != nil {
			return nil, err
		}
		groups[m.group][m.field] = v
	}
	choice, present := p["catalyst_center_as_network_collector"]
	collector := groups["applicationVisibility"]
	if present && choice != nil {
		builtin, ok := choice.(bool)
		if !ok {
			return nil, errors.New("invalid collector choice")
		}
		if builtin {
			if p["netflow_collector_ip_address"] != nil || p["netflow_collector_port"] != nil {
				return nil, errors.New("builtin collector must omit external address/port")
			}
			collector["collectorType"] = "Builtin"
		} else {
			collector["collectorType"] = "TelemetryBrokerOrUDPDirector"
			if value := p["netflow_collector_ip_address"]; value != nil {
				ip, ok := value.(string)
				if !ok {
					return nil, errors.New("invalid external collector address")
				}
				addr, err := netip.ParseAddr(ip)
				if err != nil || addr.Zone() != "" {
					return nil, errors.New("invalid external collector address")
				}
				collector["address"] = addr.String()
			}
			if value := p["netflow_collector_port"]; value != nil {
				port, ok := value.(int)
				if !ok || port < 1 || port > 65535 {
					return nil, errors.New("invalid external collector port")
				}
				collector["port"] = port
			}
		}
	} else if p["netflow_collector_ip_address"] != nil || p["netflow_collector_port"] != nil {
		return nil, errors.New("external collector fields require an explicit resolved collector choice")
	}
	for group, fields := range groups {
		if len(fields) == 0 {
			delete(groups, group)
		}
	}
	if len(groups) == 0 {
		return []networkSetting{}, nil
	}

	return []networkSetting{{kind: "telemetry", telemetry: groups}}, nil
}

func parseTelemetryObservation(response map[string]json.RawMessage) (map[string]map[string]any, error) {
	groups := map[string]map[string]any{}
	knownGroups := map[string]bool{}
	for _, group := range telemetryGroups {
		knownGroups[group] = true
	}
	for group := range response {
		if !knownGroups[group] {
			return nil, errInvalidResponse
		}
	}
	for _, group := range telemetryGroups {
		raw, ok := response[group]
		if !ok {
			return nil, errInvalidResponse
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			groups[group] = nil
			continue
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return nil, errInvalidResponse
		}
		out := map[string]any{}
		groups[group] = out
		// Empty objects remain distinct from explicit false/empty lists.
		if len(fields) == 0 {
			continue
		}
		for _, key := range []string{"inheritedSiteId", "inheritedSiteName"} {
			if value, ok := fields[key]; ok {
				var identity string
				if json.Unmarshal(value, &identity) != nil || identity != "" {
					return nil, errInvalidResponse
				}
			}
		}
		boolKey := ""
		switch group {
		case "wiredDataCollection":
			boolKey = "enableWiredDataCollection"
		case "wirelessTelemetry":
			boolKey = "enableWirelessTelemetry"
		case "snmpTraps":
			boolKey = "useBuiltinTrapServer"
		case "syslogs":
			boolKey = "useBuiltinSyslogServer"
		case "applicationVisibility":
			boolKey = "enableOnWiredAccessDevices"
		}
		allowed := map[string]bool{boolKey: true, "inheritedSiteId": true, "inheritedSiteName": true}
		if group == "snmpTraps" {
			allowed["externalTrapServers"] = true
		}
		if group == "syslogs" {
			allowed["externalSyslogServers"] = true
		}
		if group == "applicationVisibility" {
			allowed["collector"] = true
		}
		for field := range fields {
			if !allowed[field] {
				return nil, errInvalidResponse
			}
		}
		var value *bool
		if json.Unmarshal(fields[boolKey], &value) != nil || value == nil {
			return nil, errInvalidResponse
		}
		out[boolKey] = *value
		if group == "snmpTraps" || group == "syslogs" {
			key := "externalTrapServers"
			if group == "syslogs" {
				key = "externalSyslogServers"
			}
			// Omission is not proof of an empty server list.
			var list any
			if json.Unmarshal(fields[key], &list) != nil {
				return nil, errInvalidResponse
			}
			servers, err := parseServerList(list, "telemetry")
			if err != nil {
				return nil, errInvalidResponse
			}
			out[key] = servers
		}
		if group == "applicationVisibility" {
			var collector map[string]json.RawMessage
			if json.Unmarshal(fields["collector"], &collector) != nil || collector == nil {
				return nil, errInvalidResponse
			}
			for field := range collector {
				if field != "collectorType" && field != "address" && field != "port" {
					return nil, errInvalidResponse
				}
			}
			var kind string
			if !readSettingString(collector, "collectorType", &kind) {
				return nil, errInvalidResponse
			}
			out["collectorType"] = kind
			switch kind {
			case "Builtin":
			case "TelemetryBrokerOrUDPDirector":
				var ip string
				var port int
				if !readSettingString(collector, "address", &ip) || json.Unmarshal(collector["port"], &port) != nil || port < 1 || port > 65535 {
					return nil, errInvalidResponse
				}
				addr, err := netip.ParseAddr(ip)
				if err != nil || addr.Zone() != "" {
					return nil, errInvalidResponse
				}
				out["address"] = addr.String()
				out["port"] = port
			default:
				return nil, errInvalidResponse
			}
		}
	}
	return groups, nil
}

func telemetryDrift(path string, want, got map[string]map[string]any) []configapi.NetworkControllerDriftEntry {
	out := []configapi.NetworkControllerDriftEntry{}
	for _, group := range telemetryGroups {
		different := false
		for field, value := range want[group] {
			if !reflect.DeepEqual(value, got[group][field]) {
				different = true
				break
			}
		}
		if different {
			out = append(out, configapi.NetworkControllerDriftEntry{Section: "network_settings", Resource: path, Path: "/telemetry/" + group, Change: configapi.NetworkControllerDriftChangeUpdate})
		}
	}
	return out
}

// The native PUT resets omitted groups. Preserve the complete profile from
// the same frozen effective observation, while desired drift owns only its mask.
// Unknown companion fields cannot be filled with invented false/empty defaults.
func resolveTelemetrySubmission(want networkSetting, got networkObservation) (networkSetting, error) {
	out := want
	out.telemetry = cloneNetworkTelemetry(got.telemetry)
	if out.telemetry == nil {
		out.telemetry = map[string]map[string]any{}
	}
	baseline := out.telemetry
	for group, fields := range want.telemetry {
		merged := baseline[group]
		if merged == nil {
			merged = map[string]any{}
		}
		for key, value := range fields {
			merged[key] = value
		}
		if group == "applicationVisibility" && merged["collectorType"] == "Builtin" {
			delete(merged, "address")
			delete(merged, "port")
		}
		out.telemetry[group] = merged
	}
	raw, err := networkSettingPayload(out)
	if err != nil {
		return networkSetting{}, err
	}
	var response map[string]json.RawMessage
	if json.Unmarshal(raw, &response) != nil {
		return networkSetting{}, errInvalidResponse
	}
	for _, group := range telemetryGroups {
		if _, present := response[group]; !present {
			response[group] = json.RawMessage("null")
		}
	}
	parsed, err := parseTelemetryObservation(response)
	if err != nil {
		return networkSetting{}, err
	}
	for _, group := range telemetryGroups {
		if len(parsed[group]) == 0 {
			return networkSetting{}, errInvalidResponse
		}
	}
	return out, nil
}
