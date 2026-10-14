// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"errors"
	"fmt"
	"net/netip"
)

var vpnDNSKeys = []string{
	"ipv4_dns_hosts", "ipv6_dns_hosts",
	"ipv4_primary_dns_server", "ipv4_primary_dns_server_variable",
	"ipv4_secondary_dns_server", "ipv4_secondary_dns_server_variable",
	"ipv6_primary_dns_server", "ipv6_primary_dns_server_variable", "ipv6_secondary_dns_server", "ipv6_secondary_dns_server_variable",
}

func vpnDNSLeaf(value, variable any, objectType string) (map[string]any, error) {
	if value != nil && variable != nil {
		return nil, errors.New("DNS literal and variable are mutually exclusive")
	}
	if variable != nil {
		name, ok := variable.(string)
		if !ok || name == "" {
			return nil, errors.New("invalid DNS variable")
		}
		return map[string]any{"vipObjectType": objectType, "vipType": "variableName", "vipVariableName": name}, nil
	}
	if value == nil {
		return nil, errors.New("DNS value or variable is required")
	}
	if objectType == "object" {
		text, ok := value.(string)
		if !ok || text == "" {
			return nil, errors.New("DNS scalar must be a nonempty string")
		}
	} else if _, ok := value.([]any); !ok {
		return nil, errors.New("DNS list must be an array")
	}
	return map[string]any{"vipObjectType": objectType, "vipType": "constant", "vipValue": value}, nil
}

func prepareVPNDNS(input map[string]any) (map[string]any, map[string]any, error) {
	prepared := make(map[string]any, len(input))
	for key, value := range input {
		prepared[key] = value
	}
	for _, key := range vpnDNSKeys {
		delete(prepared, key)
	}
	if input["ipv6_primary_dns_server_variable"] != nil || input["ipv6_secondary_dns_server_variable"] != nil {
		return nil, nil, errors.New("pinned provider omits IPv6 DNS server address variables")
	}
	trees := map[string]any{}
	for _, family := range []string{"ipv4", "ipv6"} {
		path := "dns"
		if family == "ipv6" {
			path = "dns-ipv6"
		}
		rows := []any{}
		seen := map[string]bool{}
		for _, role := range []string{"primary", "secondary"} {
			base := family + "_" + role + "_dns_server"
			address, variable := input[base], input[base+"_variable"]
			if address == nil && variable == nil {
				continue
			}
			if family == "ipv6" && variable != nil {
				return nil, nil, errors.New("IPv6 DNS server address variables are unsupported by the provider")
			}
			if address != nil {
				value, ok := address.(string)
				parsed, err := netip.ParseAddr(value)
				if !ok || err != nil || (family == "ipv4" && !parsed.Is4()) || (family == "ipv6" && !parsed.Is6()) {
					return nil, nil, fmt.Errorf("invalid %s DNS server", family)
				}
				if seen[value] {
					return nil, nil, errors.New("duplicate DNS server")
				}
				seen[value] = true
			}
			leaf, err := vpnDNSLeaf(address, variable, "object")
			if err != nil {
				return nil, nil, err
			}
			rows = append(rows, map[string]any{"dns-addr": leaf, "role": map[string]any{"vipObjectType": "object", "vipType": "constant", "vipValue": role}})
		}
		if len(rows) > 0 {
			trees[path] = map[string]any{"vipObjectType": "tree", "vipType": "constant", "vipPrimaryKey": []string{"dns-addr"}, "vipValue": rows}
		}
	}
	hosts := []any{}
	seenHosts := map[string]bool{}
	for _, family := range []string{"ipv4", "ipv6"} {
		raw := input[family+"_dns_hosts"]
		if raw == nil {
			continue
		}
		items, ok := raw.([]any)
		if !ok {
			return nil, nil, errors.New("DNS hosts must be an array")
		}
		for _, item := range items {
			host, ok := item.(map[string]any)
			if !ok {
				return nil, nil, errors.New("DNS host must be an object")
			}
			for key := range host {
				if key != "hostname" && key != "hostname_variable" && key != "ips" && key != "ips_variable" && key != "optional" {
					return nil, nil, fmt.Errorf("unsupported DNS host field %s", key)
				}
			}
			name, err := vpnDNSLeaf(host["hostname"], host["hostname_variable"], "object")
			if err != nil {
				return nil, nil, err
			}
			identity := fmt.Sprint(name["vipValue"], name["vipVariableName"])
			if seenHosts[identity] {
				return nil, nil, errors.New("duplicate DNS hostname")
			}
			seenHosts[identity] = true
			if host["ips"] != nil {
				ips, ok := host["ips"].([]any)
				if !ok || len(ips) == 0 {
					return nil, nil, errors.New("DNS host IPs must be a nonempty array")
				}
				for _, ip := range ips {
					value, ok := ip.(string)
					parsed, err := netip.ParseAddr(value)
					if !ok || err != nil || (family == "ipv4" && !parsed.Is4()) || (family == "ipv6" && !parsed.Is6()) {
						return nil, nil, errors.New("invalid DNS host IP")
					}
				}
			}
			addresses, err := vpnDNSLeaf(host["ips"], host["ips_variable"], "list")
			if err != nil {
				return nil, nil, err
			}
			row := map[string]any{"hostname": name, "ip": addresses}
			if raw := host["optional"]; raw != nil {
				optional, ok := raw.(bool)
				if !ok {
					return nil, nil, errors.New("DNS host optional must be Boolean")
				}
				row["vipOptional"] = optional
				row["priority-order"] = []string{"hostname", "ip"}
			}
			hosts = append(hosts, row)
		}
	}
	if len(hosts) > 0 {
		trees["host"] = map[string]any{"vipObjectType": "tree", "vipType": "constant", "vipPrimaryKey": []string{"hostname"}, "vipValue": hosts}
	}
	return prepared, trees, nil
}
