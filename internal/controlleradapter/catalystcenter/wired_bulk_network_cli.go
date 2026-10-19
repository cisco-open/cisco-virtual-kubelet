// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"net/netip"
	"reflect"
	"sort"
	"strings"
)

// Bounded IOS-XE observations for declared network settings. This is not a
// general renderer or full-device convergence provider. Other settings require
// their own qualified device semantics before this stage can consume them.
func wiredBulkNetworkCommands(settings []networkSetting) ([]string, error) {
	if len(settings) == 0 {
		return nil, errWiredBulkNetworkContextUnavailable
	}
	commands := []string{"show version"}
	seen := map[string]bool{}
	for _, want := range settings {
		if seen[want.kind] {
			return nil, errConfigurationOwned
		}
		seen[want.kind] = true
		switch want.kind {
		case "ntp":
			commands = append(commands, "show running-config | include ^ntp server")
		case "dns":
			commands = append(commands, "show running-config | include ^ip name-server", "show running-config | include ^ip domain.name")
		case "banner":
			if want.bannerType != "Custom" || !validBanner(want.banner) {
				return nil, errWiredBulkNetworkContextUnavailable
			}
			commands = append(commands, "show running-config | section ^banner motd")
		case "timezone":
			if !wiredBulkFixedUTCZone(want.timezone) {
				return nil, errWiredBulkNetworkContextUnavailable
			}
			commands = append(commands, "show running-config all | include ^clock")
		default:
			return nil, errWiredBulkNetworkContextUnavailable
		}
	}
	return commands, nil
}

func verifyWiredBulkNetworkCLI(settings []networkSetting, outputs []string) error {
	commands, err := wiredBulkNetworkCommands(settings)
	if err != nil {
		return err
	}
	if len(outputs) != len(commands) {
		return errInvalidResponse
	}
	// Chassis identity is independently checked by the shared worker collector.
	index := 1
	for _, want := range settings {
		switch want.kind {
		case "ntp":
			servers := []string{}
			seen := map[string]bool{}
			for _, line := range strings.Split(outputs[index], "\n") {
				fields := strings.Fields(line)
				if len(fields) == 0 {
					continue
				}
				if len(fields) != 3 || fields[0] != "ntp" || fields[1] != "server" {
					return errInvalidResponse
				}
				server := fields[2]
				if ip, err := netip.ParseAddr(server); err == nil && ip.Zone() == "" {
					server = ip.String()
				} else if validNetworkName(server) {
					server = strings.ToLower(strings.TrimSuffix(server, "."))
				} else {
					return errInvalidResponse
				}
				if seen[server] {
					return errInvalidResponse
				}
				seen[server] = true
				servers = append(servers, server)
			}
			sort.Strings(servers)
			if !equalNetworkServers(servers, want.servers) {
				return errConfigurationStale
			}
			index++
		case "dns":
			servers := []string{}
			seen := map[string]bool{}
			for _, line := range strings.Split(outputs[index], "\n") {
				fields := strings.Fields(line)
				if len(fields) == 0 {
					continue
				}
				if len(fields) < 3 || fields[0] != "ip" || fields[1] != "name-server" {
					return errInvalidResponse
				}
				for _, server := range fields[2:] {
					ip, err := netip.ParseAddr(server)
					if err != nil || ip.Zone() != "" || seen[ip.String()] {
						return errInvalidResponse
					}
					seen[ip.String()] = true
					servers = append(servers, ip.String())
				}
			}
			if !equalNetworkServers(servers, want.servers) {
				return errConfigurationStale
			}
			domain := ""
			for _, line := range strings.Split(outputs[index+1], "\n") {
				fields := strings.Fields(line)
				if len(fields) == 0 {
					continue
				}
				if domain != "" {
					return errInvalidResponse
				}
				if len(fields) == 4 && strings.Join(fields[:3], " ") == "ip domain name" {
					domain = fields[3]
				} else if len(fields) == 3 && strings.Join(fields[:2], " ") == "ip domain-name" {
					domain = fields[2]
				} else {
					return errInvalidResponse
				}
			}
			if domain != want.domain {
				return errConfigurationStale
			}
			index += 2
		case "banner":
			if err := verifyWiredBulkBannerCLI(want.banner, outputs[index]); err != nil {
				return err
			}
			index++
		case "timezone":
			if err := verifyWiredBulkUTCClockCLI(outputs[index]); err != nil {
				return err
			}
			index++
		}
	}
	return nil
}

func equalNetworkServers(a, b []string) bool {
	return reflect.DeepEqual(a, b) || len(a) == 0 && len(b) == 0
}
