// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

type systemSettingsReportIntent struct {
	external *externalAuthIntent
	servers  []authenticationPolicyServer
}

func parseSystemSettingsReport(raw []byte) (systemSettingsReportIntent, error) {
	var result systemSettingsReportIntent
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return result, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return result, errors.New("system settings require a section-specific model")
	}
	settings, ok := root["system_settings"].(map[string]any)
	if !ok || len(settings) == 0 {
		return result, errors.New("invalid system settings")
	}
	for key, value := range settings {
		switch key {
		case "external_authentication":
			projection, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"system_settings": map[string]any{key: value}}})
			external, err := parseExternalAuth(projection)
			if err != nil {
				return result, err
			}
			result.external = &external
		case "authentication_and_policy_servers":
			section, ok := value.(map[string]any)
			if !ok || len(section) == 0 || len(section) > 2 {
				return result, errors.New("invalid policy server metadata section")
			}
			for key := range section {
				if key != "aaa" && key != "ise" {
					return result, errors.New("unsupported policy server metadata section")
				}
			}
			list := []any{}
			if value, present := section["aaa"]; present {
				var valid bool
				list, valid = value.([]any)
				if !valid || len(list) == 0 || len(list) > 1000 {
					return result, errors.New("invalid AAA server collection")
				}
			}
			seen := map[string]bool{}
			if value, present := section["ise"]; present {
				server, err := parseISEPolicyServerReport(value)
				if err != nil {
					return result, err
				}
				result.servers = append(result.servers, server)
				seen[server.IP] = true
			}
			for _, entry := range list {
				server, err := parsePolicyServerMetadata(entry)
				if err != nil {
					return result, err
				}
				if seen[server.IP] {
					return result, errors.New("duplicate AAA server address")
				}
				seen[server.IP] = true
				result.servers = append(result.servers, server)
			}
		default:
			return result, errors.New("unsupported system settings field")
		}
	}
	return result, nil
}
func policyServerInteger(value any, min, max int) (*int, error) {
	if value == nil {
		return nil, errors.New("invalid AAA numeric field")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("invalid AAA numeric field")
	}
	var n int
	if json.Unmarshal(raw, &n) != nil || n < min || n > max {
		return nil, errors.New("invalid AAA numeric field")
	}
	return &n, nil
}
func parsePolicyServerMetadata(value any) (authenticationPolicyServer, error) {
	disabled := false
	server := authenticationPolicyServer{ISE: &disabled, Role: "primary"}
	object, ok := value.(map[string]any)
	if !ok {
		return server, errors.New("invalid AAA server")
	}
	for field, value := range object {
		var err error
		switch field {
		case "ip_address":
			ip, ok := value.(string)
			if !ok {
				return server, errors.New("invalid AAA address")
			}
			server.IP, err = canonicalAAAAddress(ip)
		case "retries":
			server.Retries, err = policyServerInteger(value, 0, 10)
		case "timeout":
			server.Timeout, err = policyServerInteger(value, 1, 60)
		case "protocols":
			protocols, ok := value.(map[string]any)
			if !ok || len(protocols) == 0 {
				return server, errors.New("AAA protocols must be explicit")
			}
			radius, tacacs := false, false
			for protocol, value := range protocols {
				settings, ok := value.(map[string]any)
				if !ok {
					return server, errors.New("invalid AAA protocol settings")
				}
				switch protocol {
				case "radius":
					radius = true
					for key, value := range settings {
						switch key {
						case "authentication_port":
							server.AuthenticationPort, err = policyServerInteger(value, 1, 65535)
						case "accounting_port":
							server.AccountingPort, err = policyServerInteger(value, 1, 65535)
						default:
							return server, errors.New("unsupported RADIUS field; key wrapping is not yet qualified")
						}
						if err != nil {
							return server, err
						}
					}
				case "tacacs":
					tacacs = true
					for key, value := range settings {
						if key != "port" {
							return server, errors.New("unsupported TACACS field")
						}
						server.Port, err = policyServerInteger(value, 1, 65535)
						if err != nil {
							return server, err
						}
					}
				default:
					return server, errors.New("unsupported AAA protocol")
				}
			}
			switch {
			case radius && tacacs:
				server.Protocol = "RADIUS_TACACS"
			case radius:
				server.Protocol = "RADIUS"
			case tacacs:
				server.Protocol = "TACACS"
			}
		default:
			return server, errors.New("unsupported AAA server field; credential values and versions cannot be compared as metadata")
		}
		if err != nil {
			return server, err
		}
	}
	if server.IP == "" || server.Protocol == "" {
		return server, errors.New("AAA address and protocols are required")
	}
	return server, nil
}
func (i systemSettingsReportIntent) count() int {
	count := len(i.servers)
	for _, server := range i.servers {
		if server.ISE != nil && *server.ISE {
			count++
		}
	}
	if i.external != nil {
		count += (&externalAuthExecutor{desired: *i.external}).count()
	}
	return count
}
func (c *client) reportSystemSettings(ctx context.Context, intent systemSettingsReportIntent) ([]configapi.NetworkControllerDriftEntry, error) {
	out := []configapi.NetworkControllerDriftEntry{}
	if len(intent.servers) > 0 {
		actual, err := c.authenticationPolicyServers(ctx)
		if err != nil {
			return nil, err
		}
		for _, desired := range intent.servers {
			observed, exists := actual[desired.IP]
			path := "/authentication_and_policy_servers/aaa"
			ise := desired.ISE != nil && *desired.ISE
			if ise {
				path = "/authentication_and_policy_servers/ise"
			}
			change := configapi.NetworkControllerDriftChangeUpdate
			if !exists {
				change = configapi.NetworkControllerDriftChangeAdd
			}
			if !exists || !policyServerMatches(desired, observed) {
				out = append(out, configapi.NetworkControllerDriftEntry{Section: "system_settings", Resource: desired.IP, Path: path, Change: change})
			}
			if ise {
				complete := false
				if exists && observed.ISE != nil && *observed.ISE {
					state, err := c.policyISEIntegration(ctx, observed.ID)
					if err != nil {
						return nil, err
					}
					complete = state.Status == "COMPLETE"
				}
				if !complete {
					out = append(out, configapi.NetworkControllerDriftEntry{Section: "system_settings", Resource: policyISEIntegrationResource + desired.IP, Path: path, Change: change})
				}
			}
		}
	}
	if intent.external != nil {
		drift, err := (&externalAuthExecutor{client: c, desired: *intent.external}).report(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, drift...)
	}
	return out, nil
}
