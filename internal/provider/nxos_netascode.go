// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package provider

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/intent"
)

var (
	nxosMergeRules = intent.KeyRules{
		"vlan.vlans":                    "id",
		"interfaces.ethernets":          "id",
		"interface_ethernet.interfaces": "name",
	}
	nxosVariablePattern = regexp.MustCompile(`\$\{([A-Za-z0-9_.-]+)\}`)
)

// NormalizeNXOSNetAsCodeSource resolves the supported NX-OS NetAsCode source
// shapes to the per-device family map consumed by NXOSConfig writers.
func NormalizeNXOSNetAsCodeSource(config map[string]any, deviceName string) (map[string]any, error) {
	return normalizeNXOSNetAsCodeSource(config, deviceName)
}

func normalizeNXOSNetAsCodeSource(config map[string]any, deviceName string) (map[string]any, error) {
	if config == nil {
		return map[string]any{}, nil
	}
	config, err := nxosConfigurationBlock(config, deviceName)
	if err != nil {
		return nil, err
	}
	out := cloneMap(config)
	if err := normalizeNXOSEthernetSource(out); err != nil {
		return nil, err
	}
	return out, nil
}

func nxosConfigurationBlock(config map[string]any, deviceName string) (map[string]any, error) {
	if nxosBody, ok := asMap(config["nxos"]); ok {
		return resolveNXOSEnvelope(nxosBody, deviceName)
	}
	if _, hasDevices := config["devices"]; hasDevices {
		return resolveNXOSEnvelope(config, deviceName)
	}
	if cfg, ok := asMap(config["configuration"]); ok {
		return cfg, nil
	}
	return config, nil
}

func resolveNXOSEnvelope(root map[string]any, deviceName string) (map[string]any, error) {
	templates, err := nxosTemplates(root)
	if err != nil {
		return nil, err
	}
	device, err := nxosDevice(root, deviceName)
	if err != nil {
		return nil, err
	}
	groups, err := nxosDeviceGroups(root, deviceName, device)
	if err != nil {
		return nil, err
	}
	vars, err := nxosMergedVariables(root, groups, device)
	if err != nil {
		return nil, err
	}
	resolved := map[string]any{}
	if global, ok := asMap(root["global"]); ok {
		resolved = nxosMergeConfig(resolved, scopedConfiguration(global))
		cfg, err := nxosTemplateConfig(global["templates"], templates)
		if err != nil {
			return nil, err
		}
		resolved = nxosMergeConfig(resolved, cfg)
	}
	for _, group := range groups {
		resolved = nxosMergeConfig(resolved, scopedConfiguration(group))
		cfg, err := nxosTemplateConfig(group["templates"], templates)
		if err != nil {
			return nil, err
		}
		resolved = nxosMergeConfig(resolved, cfg)
	}
	if device != nil {
		resolved = nxosMergeConfig(resolved, scopedConfiguration(device))
		cfg, err := nxosTemplateConfig(device["templates"], templates)
		if err != nil {
			return nil, err
		}
		resolved = nxosMergeConfig(resolved, cfg)
	}
	if err := applyNXOSInterfaceGroups(resolved, root); err != nil {
		return nil, err
	}
	rendered, err := nxosRenderVariables(resolved, vars)
	if err != nil {
		return nil, err
	}
	out, ok := asMap(rendered)
	if !ok {
		return nil, fmt.Errorf("resolved NX-OS configuration must be a mapping")
	}
	return out, nil
}

func nxosDevice(root map[string]any, deviceName string) (map[string]any, error) {
	devices, ok := asList(root["devices"])
	if !ok {
		return nil, nil
	}
	for i, item := range devices {
		dev, ok := asMap(item)
		if !ok {
			return nil, fmt.Errorf("devices[%d] must be a mapping", i)
		}
		if nxosName(dev) == deviceName {
			return dev, nil
		}
	}
	return nil, fmt.Errorf("device %q not present under nxos.devices", deviceName)
}

func nxosDeviceGroups(root map[string]any, deviceName string, device map[string]any) ([]map[string]any, error) {
	rawGroups, ok := asList(root["device_groups"])
	if !ok {
		return nil, nil
	}
	byName := map[string]map[string]any{}
	var ordered []map[string]any
	for i, item := range rawGroups {
		group, ok := asMap(item)
		if !ok {
			return nil, fmt.Errorf("device_groups[%d] must be a mapping", i)
		}
		name := nxosName(group)
		if name == "" {
			return nil, fmt.Errorf("device_groups[%d].name is required", i)
		}
		byName[name] = group
		ordered = append(ordered, group)
	}
	var out []map[string]any
	seen := map[string]struct{}{}
	if device != nil {
		for _, name := range nxosStringList(device["device_groups"]) {
			group, ok := byName[name]
			if !ok {
				return nil, fmt.Errorf("device %q references unknown device_group %q", deviceName, name)
			}
			out = append(out, group)
			seen[name] = struct{}{}
		}
	}
	for _, group := range ordered {
		name := nxosName(group)
		if _, ok := seen[name]; ok {
			continue
		}
		if stringListContains(nxosStringList(group["devices"]), deviceName) {
			out = append(out, group)
			seen[name] = struct{}{}
		}
	}
	return out, nil
}

func nxosMergedVariables(root map[string]any, groups []map[string]any, device map[string]any) (map[string]string, error) {
	vars := map[string]string{}
	if global, ok := asMap(root["global"]); ok {
		if err := mergeNXOSVariables(vars, global["variables"], "global.variables"); err != nil {
			return nil, err
		}
	}
	for _, group := range groups {
		if err := mergeNXOSVariables(vars, group["variables"], "device_groups."+nxosName(group)+".variables"); err != nil {
			return nil, err
		}
	}
	if device != nil {
		if err := mergeNXOSVariables(vars, device["variables"], "devices."+nxosName(device)+".variables"); err != nil {
			return nil, err
		}
	}
	return vars, nil
}

func mergeNXOSVariables(dst map[string]string, raw any, origin string) error {
	if raw == nil {
		return nil
	}
	vars, ok := asMap(raw)
	if !ok {
		return fmt.Errorf("%s must be a mapping", origin)
	}
	for _, key := range sortedAnyMapKeys(vars) {
		dst[key] = nxosScalarString(vars[key])
	}
	return nil
}

func nxosTemplates(root map[string]any) (map[string]map[string]any, error) {
	raw, ok := asList(root["templates"])
	if !ok {
		return nil, nil
	}
	out := map[string]map[string]any{}
	for i, item := range raw {
		tpl, ok := asMap(item)
		if !ok {
			return nil, fmt.Errorf("templates[%d] must be a mapping", i)
		}
		name := nxosName(tpl)
		if name == "" {
			return nil, fmt.Errorf("templates[%d].name is required", i)
		}
		typ := strings.TrimSpace(strings.ToLower(nxosScalarString(tpl["type"])))
		if typ == "" {
			typ = "model"
		}
		if typ != "model" {
			return nil, fmt.Errorf("template %q type %q is not supported by NXOSConfig; use resolved model intent", name, typ)
		}
		if _, ok := asMap(tpl["configuration"]); !ok {
			return nil, fmt.Errorf("template %q has no model configuration", name)
		}
		out[name] = tpl
	}
	return out, nil
}

func nxosTemplateConfig(rawRefs any, templates map[string]map[string]any) (map[string]any, error) {
	refs := nxosStringList(rawRefs)
	if len(refs) == 0 {
		return nil, nil
	}
	type selected struct {
		name  string
		order int
		tpl   map[string]any
	}
	selectedTemplates := make([]selected, 0, len(refs))
	for _, name := range refs {
		tpl, ok := templates[name]
		if !ok {
			return nil, fmt.Errorf("template reference %q is not defined", name)
		}
		selectedTemplates = append(selectedTemplates, selected{name: name, order: nxosInt(tpl["order"]), tpl: tpl})
	}
	sort.SliceStable(selectedTemplates, func(i, j int) bool {
		return selectedTemplates[i].order < selectedTemplates[j].order
	})
	out := map[string]any{}
	for _, item := range selectedTemplates {
		out = nxosMergeConfig(out, scopedConfiguration(item.tpl))
	}
	return out, nil
}

func normalizeNXOSEthernetSource(config map[string]any) error {
	interfaces, ok := asMap(config["interfaces"])
	if !ok {
		return nil
	}
	ethernets, hasEthernets := interfaces["ethernets"]
	if !hasEthernets {
		return nil
	}
	if _, exists := config["interface_ethernet"]; exists {
		return fmt.Errorf("source contains both interface_ethernet and interfaces.ethernets")
	}
	list, ok := asList(ethernets)
	if !ok {
		return fmt.Errorf("interfaces.ethernets must be a list")
	}
	normalized := make([]any, 0, len(list))
	for i, item := range list {
		intf, ok := asMap(item)
		if !ok {
			return fmt.Errorf("interfaces.ethernets[%d] must be a mapping", i)
		}
		copied := cloneMap(intf)
		if _, hasName := copied["name"]; !hasName {
			if id, hasID := copied["id"]; hasID {
				copied["name"] = id
			}
		}
		if _, hasType := copied["type"]; !hasType {
			copied["type"] = "Ethernet"
		}
		normalized = append(normalized, copied)
	}
	config["interface_ethernet"] = map[string]any{"interfaces": normalized}
	remaining := cloneMap(interfaces)
	delete(remaining, "ethernets")
	if len(remaining) == 0 {
		delete(config, "interfaces")
	} else {
		config["interfaces"] = remaining
	}
	return nil
}

func applyNXOSInterfaceGroups(config, root map[string]any) error {
	groupDefs, err := nxosInterfaceGroups(root)
	if err != nil {
		return err
	}
	if len(groupDefs) == 0 {
		return nil
	}
	interfaces, ok := asMap(config["interfaces"])
	if !ok {
		return nil
	}
	for family, raw := range interfaces {
		list, ok := asList(raw)
		if !ok {
			continue
		}
		out := make([]any, 0, len(list))
		for i, item := range list {
			entry, ok := asMap(item)
			if !ok {
				return fmt.Errorf("interfaces.%s[%d] must be a mapping", family, i)
			}
			expanded := map[string]any{}
			for _, groupName := range nxosStringList(entry["interface_groups"]) {
				group, ok := groupDefs[groupName]
				if !ok {
					return fmt.Errorf("interfaces.%s[%d] references unknown interface_group %q", family, i, groupName)
				}
				expanded = nxosMergeConfig(expanded, group)
			}
			entryCopy := cloneMap(entry)
			delete(entryCopy, "interface_groups")
			expanded = nxosMergeConfig(expanded, entryCopy)
			out = append(out, expanded)
		}
		interfaces[family] = out
	}
	return nil
}

func nxosInterfaceGroups(root map[string]any) (map[string]map[string]any, error) {
	rawGroups, ok := asList(root["interface_groups"])
	if !ok {
		return nil, nil
	}
	out := map[string]map[string]any{}
	for i, item := range rawGroups {
		group, ok := asMap(item)
		if !ok {
			return nil, fmt.Errorf("interface_groups[%d] must be a mapping", i)
		}
		name := nxosName(group)
		if name == "" {
			return nil, fmt.Errorf("interface_groups[%d].name is required", i)
		}
		cfg, ok := asMap(group["configuration"])
		if !ok {
			cfg = map[string]any{}
		}
		out[name] = cfg
	}
	return out, nil
}

func nxosMergeConfig(left, right map[string]any) map[string]any {
	if left == nil {
		left = map[string]any{}
	}
	if right == nil {
		return cloneMap(left)
	}
	merged := intent.MergeWithRules(left, right, nxosMergeRules)
	if out, ok := asMap(merged); ok {
		return out
	}
	return map[string]any{}
}

func scopedConfiguration(scope map[string]any) map[string]any {
	cfg, ok := asMap(scope["configuration"])
	if !ok {
		return nil
	}
	return cloneMap(cfg)
}

func nxosRenderVariables(v any, vars map[string]string) (any, error) {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for key, val := range x {
			rendered, err := nxosRenderVariables(val, vars)
			if err != nil {
				return nil, err
			}
			out[key] = rendered
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(x))
		for _, val := range x {
			rendered, err := nxosRenderVariables(val, vars)
			if err != nil {
				return nil, err
			}
			out = append(out, rendered)
		}
		return out, nil
	case string:
		missing := ""
		out := nxosVariablePattern.ReplaceAllStringFunc(x, func(match string) string {
			name := nxosVariablePattern.FindStringSubmatch(match)[1]
			val, ok := vars[name]
			if !ok {
				missing = name
				return match
			}
			return val
		})
		if missing != "" {
			return nil, fmt.Errorf("unresolved variable %q", missing)
		}
		return out, nil
	default:
		return v, nil
	}
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneAny(v)
	}
	return out
}

func cloneAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return cloneMap(x)
	case []any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			out = append(out, cloneAny(item))
		}
		return out
	default:
		return v
	}
}

func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func asList(v any) ([]any, bool) {
	l, ok := v.([]any)
	return l, ok
}

func nxosStringList(v any) []string {
	switch x := v.(type) {
	case nil:
		return nil
	case []string:
		out := append([]string(nil), x...)
		return out
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			s := strings.TrimSpace(nxosScalarString(item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if strings.TrimSpace(x) == "" {
			return nil
		}
		return []string{strings.TrimSpace(x)}
	default:
		return []string{strings.TrimSpace(nxosScalarString(x))}
	}
}

func nxosScalarString(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func nxosName(m map[string]any) string {
	return strings.TrimSpace(nxosScalarString(m["name"]))
}

func nxosInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		var out int
		_, _ = fmt.Sscanf(strings.TrimSpace(x), "%d", &out)
		return out
	default:
		return 0
	}
}

func stringListContains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func sortedAnyMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
