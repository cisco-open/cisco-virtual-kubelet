// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package sonic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
)

func (d *SONICDriver) GetCDPNeighbors(context.Context) ([]common.CDPNeighbor, error) { return nil, nil }
func (d *SONICDriver) GetOSPFNeighbors(context.Context) ([]common.OSPFNeighbor, error) {
	return nil, nil
}
func (d *SONICDriver) GetHostedApps(context.Context) ([]common.HostedApp, error) { return nil, nil }

func (d *SONICDriver) GetInterfaceStats(ctx context.Context) ([]common.InterfaceStats, error) {
	raw, err := d.gnmi.GetJSON(ctx, "/interfaces")
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	entries, err := decodeOpenConfigInterfaces(raw)
	if err != nil {
		return nil, err
	}
	stats := make([]common.InterfaceStats, 0, len(entries))
	for _, intf := range entries {
		state := childMap(intf, "state")
		counters := childMap(state, "counters")
		name := stringValue(localValue(intf, "name"))
		if name == "" {
			name = stringValue(localValue(state, "name"))
		}
		stats = append(stats, common.InterfaceStats{
			Name:        name,
			OperStatus:  strings.ToLower(stringValue(localValue(state, "oper-status"))),
			InOctets:    uintValue(localValue(counters, "in-octets")),
			OutOctets:   uintValue(localValue(counters, "out-octets")),
			Speed:       speedValue(localValue(state, "speed")),
			IPv4Address: firstIPv4Address(intf),
		})
	}
	return stats, nil
}

func (d *SONICDriver) GetInterfaceIPs(ctx context.Context) ([]common.InterfaceIP, error) {
	stats, err := d.GetInterfaceStats(ctx)
	if err != nil {
		return nil, err
	}
	ips := make([]common.InterfaceIP, 0, len(stats))
	for _, stat := range stats {
		if stat.IPv4Address == "" {
			continue
		}
		ips = append(ips, common.InterfaceIP{Interface: stat.Name, IPv4: stat.IPv4Address, Status: stat.OperStatus})
	}
	return ips, nil
}

func decodeOpenConfigInterfaces(raw []byte) ([]map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	m, _ := root.(map[string]any)
	interfaces := childMap(m, "interfaces")
	if interfaces == nil {
		interfaces = m
	}
	list := childSlice(interfaces, "interface")
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func childMap(m map[string]any, name string) map[string]any {
	if m == nil {
		return nil
	}
	if v, ok := localValue(m, name).(map[string]any); ok {
		return v
	}
	return nil
}

func childSlice(m map[string]any, name string) []any {
	if m == nil {
		return nil
	}
	if v, ok := localValue(m, name).([]any); ok {
		return v
	}
	return nil
}

func localValue(m map[string]any, name string) any {
	for k, v := range m {
		if stripModule(k) == name {
			return v
		}
	}
	return nil
}

func stringValue(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case fmt.Stringer:
		return strings.TrimSpace(t.String())
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

func uintValue(v any) uint64 {
	switch t := v.(type) {
	case json.Number:
		u, _ := strconv.ParseUint(t.String(), 10, 64)
		return u
	case string:
		u, _ := strconv.ParseUint(strings.TrimSpace(t), 10, 64)
		return u
	case float64:
		if t > 0 {
			return uint64(t)
		}
	}
	return 0
}

var speedRe = regexp.MustCompile(`(?i)(\d+)\s*(g|m|k)?`)

func speedValue(v any) uint64 {
	if n := uintValue(v); n > 0 {
		return n
	}
	m := speedRe.FindStringSubmatch(stringValue(v))
	if len(m) < 2 {
		return 0
	}
	n, _ := strconv.ParseUint(m[1], 10, 64)
	switch strings.ToLower(m[2]) {
	case "g":
		return n * 1000 * 1000 * 1000
	case "m":
		return n * 1000 * 1000
	case "k":
		return n * 1000
	default:
		return n
	}
}

func firstIPv4Address(intf map[string]any) string {
	if state := childMap(intf, "state"); state != nil {
		if ip := stringValue(localValue(state, "ipv4")); ip != "" {
			return ip
		}
	}
	for _, containerName := range []string{"subinterfaces", "ipv4"} {
		if ip := firstAddressInContainer(childMap(intf, containerName)); ip != "" {
			return ip
		}
	}
	return ""
}

func firstAddressInContainer(m map[string]any) string {
	if m == nil {
		return ""
	}
	if addresses := childMap(m, "addresses"); addresses != nil {
		for _, item := range childSlice(addresses, "address") {
			entry, _ := item.(map[string]any)
			if ip := stringValue(localValue(entry, "ip")); ip != "" {
				return ip
			}
			if state := childMap(entry, "state"); state != nil {
				if ip := stringValue(localValue(state, "ip")); ip != "" {
					return ip
				}
			}
		}
	}
	for _, item := range childSlice(m, "subinterface") {
		entry, _ := item.(map[string]any)
		if ip := firstAddressInContainer(childMap(entry, "ipv4")); ip != "" {
			return ip
		}
	}
	return ""
}
