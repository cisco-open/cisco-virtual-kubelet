// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// These mappings follow sdwan_classic_policy_objects.tf at the pinned module
// revision and the provider's native API definitions. Public input stays NaC.
type policyListSpec struct{ endpoint, kind, field, leaf string }

var policyLists = map[string]policyListSpec{
	"preferred_color_groups":   {"preferredcolorgroup", "preferredColorGroup", "", ""},
	"app_probe_classes":        {"appprobe", "appProbe", "", ""},
	"sla_classes":              {"sla", "sla", "", ""},
	"as_path_lists":            {"aspath", "asPath", "as_paths", "asPath"},
	"color_lists":              {"color", "color", "colors", "color"},
	"standard_community_lists": {"community", "community", "standard_communities", "community"},
	"expanded_community_lists": {"expandedcommunity", "expandedCommunity", "expanded_communities", "community"},
	"extended_community_lists": {"extcommunity", "extCommunity", "extended_communities", "community"},
	"ipv4_data_prefix_lists":   {"dataprefix", "dataPrefix", "prefixes", "ipPrefix"},
	"ipv6_data_prefix_lists":   {"dataipv6prefix", "dataipv6prefix", "prefixes", "ipv6Prefix"},
	"fqdn_lists":               {"fqdn", "fqdn", "fqdns", "pattern"},
	"port_lists":               {"port", "port", "ports", "port"},
	"site_lists":               {"site", "site", "site_ids", "siteId"},
	"vpn_lists":                {"vpn", "vpn", "vpn_ids", "vpn"},
	"region_lists":             {"region", "region", "region_ids", "regionId"},
	"application_lists":        {"app", "app", "applications", "app"},
	"local_application_lists":  {"localapp", "localapp", "applications", "app"},
	"zones":                    {"zone", "zone", "vpn_ids", "vpn"},
	"ipv4_prefix_lists":        {"prefix", "prefix", "entries", "ipPrefix"},
	"ipv6_prefix_lists":        {"ipv6prefix", "ipv6prefix", "entries", "ipv6Prefix"},
	"tloc_lists":               {"tloc", "tloc", "tlocs", "tloc"},
	"class_maps":               {"class", "class", "", ""},
	"policers":                 {"policer", "policer", "", ""},
	"mirror_lists":             {"mirror", "mirror", "", ""},
}

func policyString(v any) (string, error) {
	switch x := v.(type) {
	case string:
		if x != "" {
			return x, nil
		}
	case float64:
		if x >= 0 && x == float64(int64(x)) {
			return fmt.Sprintf("%.0f", x), nil
		}
	}
	return "", errors.New("policy entry requires a nonempty string or nonnegative integer")
}
func compilePolicyObjects(raw map[string]json.RawMessage) ([]Resource, error) {
	var out []Resource
	for section, data := range raw {
		spec, ok := policyLists[section]
		if !ok {
			return nil, fmt.Errorf("unsupported policy object collection %s", section)
		}
		var objects []map[string]any
		if json.Unmarshal(data, &objects) != nil || objects == nil {
			return nil, errors.New("policy collection must be an array")
		}
		seen := map[string]bool{}
		for _, object := range objects {
			name, ok := object["name"].(string)
			if !ok || name == "" || len(name) > 32 || strings.TrimSpace(name) != name || seen[name] {
				return nil, errors.New("invalid or duplicate policy object name")
			}
			seen[name] = true
			allowed := map[string]bool{"name": true}
			entries := []any{}
			addList := func(field, leaf string) error {
				allowed[field] = true
				v, exists := object[field]
				if !exists {
					return nil
				}
				items, ok := v.([]any)
				if !ok {
					return errors.New("policy entries must be an array")
				}
				for _, item := range items {
					value, e := policyString(item)
					if e != nil {
						return e
					}
					entries = append(entries, map[string]any{leaf: value})
				}
				return nil
			}
			switch section {
			case "preferred_color_groups":
				entry := map[string]any{}
				for _, priority := range []string{"primary", "secondary", "tertiary"} {
					colorsField, pathField := priority+"_colors", priority+"_path"
					allowed[colorsField] = true
					allowed[pathField] = true
					colors := []string{}
					if v, exists := object[colorsField]; exists {
						items, ok := v.([]any)
						if !ok {
							return nil, errors.New("colors must be an array")
						}
						for _, item := range items {
							value, e := policyString(item)
							if e != nil {
								return nil, e
							}
							colors = append(colors, value)
						}
					}
					if priority == "primary" && len(colors) == 0 {
						return nil, errors.New("primary_colors is required")
					}
					preference := map[string]any{"colorPreference": strings.Join(colors, " ")}
					if v, exists := object[pathField]; exists {
						value, e := policyString(v)
						if e != nil {
							return nil, e
						}
						preference["pathPreference"] = value
					}
					entry[priority+"Preference"] = preference
				}
				entries = append(entries, entry)
			case "app_probe_classes":
				allowed["forwarding_class"] = true
				allowed["mappings"] = true
				value, e := policyString(object["forwarding_class"])
				if e != nil {
					return nil, e
				}
				mappings, ok := object["mappings"].([]any)
				if !ok {
					return nil, errors.New("mappings must be an array")
				}
				mapped := []any{}
				for _, v := range mappings {
					m, ok := v.(map[string]any)
					if !ok {
						return nil, ErrResponse
					}
					color, e := policyString(m["color"])
					if e != nil {
						return nil, e
					}
					entry := map[string]any{"color": color}
					for k, v := range m {
						switch k {
						case "color":
						case "dscp":
							n, ok := v.(float64)
							if !ok || n < 0 || n > 63 || n != float64(int64(n)) {
								return nil, errors.New("invalid DSCP")
							}
							entry[k] = n
						default:
							return nil, errors.New("unsupported probe mapping")
						}
					}
					mapped = append(mapped, entry)
				}
				entries = append(entries, map[string]any{"forwardingClass": value, "map": mapped})
			case "sla_classes":
				entry := map[string]any{}
				fields := map[string]string{"jitter_ms": "jitter", "latency_ms": "latency", "loss_percentage": "loss"}
				for field, leaf := range fields {
					allowed[field] = true
					if v, exists := object[field]; exists {
						value, e := policyString(v)
						if e != nil {
							return nil, e
						}
						entry[leaf] = value
					}
				}
				// Named probe references are resolved against managed resources at dispatch.
				allowed["app_probe_class"] = true
				fallback := map[string]any{}
				for field, leaf := range map[string]string{"fallback_best_tunnel_criteria": "criteria", "fallback_best_tunnel_jitter": "jitterVariance", "fallback_best_tunnel_latency": "latencyVariance", "fallback_best_tunnel_loss": "lossVariance"} {
					allowed[field] = true
					if v, exists := object[field]; exists {
						value, e := policyString(v)
						if e != nil {
							return nil, e
						}
						fallback[leaf] = value
					}
				}
				if _, ok := fallback["criteria"]; ok {
					for _, leaf := range []string{"jitterVariance", "latencyVariance", "lossVariance"} {
						if _, ok := fallback[leaf]; !ok {
							fallback[leaf] = nil
						}
					}
				}
				if len(fallback) > 0 {
					entry["fallbackBestTunnel"] = fallback
				}
				entries = append(entries, entry)
			case "class_maps", "policers", "mirror_lists":
				fields := map[string]string{"queue": "queue"}
				if section == "policers" {
					fields = map[string]string{"burst_bytes": "burst", "rate_bps": "rate", "exceed_action": "exceed"}
				}
				if section == "mirror_lists" {
					fields = map[string]string{"remote_destination_ip": "remoteDest", "source_ip": "source"}
				}
				entry := map[string]any{}
				for field, leaf := range fields {
					allowed[field] = true
					value, e := policyString(object[field])
					if e != nil {
						return nil, e
					}
					entry[leaf] = value
				}
				entries = append(entries, entry)
			case "ipv4_prefix_lists", "ipv6_prefix_lists", "tloc_lists":
				allowed[spec.field] = true
				items, ok := object[spec.field].([]any)
				if !ok {
					return nil, errors.New("policy entries must be an array")
				}
				fields := map[string]string{"prefix": spec.leaf, "ge": "ge", "le": "le"}
				required := []string{"prefix"}
				if section == "tloc_lists" {
					fields = map[string]string{"tloc_ip": "tloc", "color": "color", "encapsulation": "encap", "preference": "preference"}
					required = []string{"tloc_ip", "color", "encapsulation"}
				}
				for _, item := range items {
					m, ok := item.(map[string]any)
					if !ok {
						return nil, ErrResponse
					}
					entry := map[string]any{}
					for _, k := range required {
						if _, ok := m[k]; !ok {
							return nil, errors.New("missing required policy entry field")
						}
					}
					for k, v := range m {
						leaf, ok := fields[k]
						if !ok {
							return nil, errors.New("unsupported policy entry field")
						}
						value, e := policyString(v)
						if e != nil {
							return nil, e
						}
						entry[leaf] = value
					}
					entries = append(entries, entry)
				}
			default:
				if e := addList(spec.field, spec.leaf); e != nil {
					return nil, e
				}
				if section == "application_lists" || section == "local_application_lists" {
					if e := addList("application_families", "appFamily"); e != nil {
						return nil, e
					}
				}
				if section == "zones" {
					if e := addList("interfaces", "interface"); e != nil {
						return nil, e
					}
				}
				if section == "site_lists" || section == "vpn_lists" || section == "region_lists" {
					field := strings.TrimSuffix(spec.field, "s") + "_ranges"
					allowed[field] = true
					if v, exists := object[field]; exists {
						ranges, ok := v.([]any)
						if !ok {
							return nil, errors.New("policy ranges must be an array")
						}
						for _, v := range ranges {
							m, ok := v.(map[string]any)
							if !ok || len(m) != 2 {
								return nil, errors.New("invalid policy range")
							}
							from, e := policyString(m["from"])
							if e != nil {
								return nil, e
							}
							to, e := policyString(m["to"])
							if e != nil {
								return nil, e
							}
							entries = append(entries, map[string]any{spec.leaf: from + "-" + to})
						}
					}
					if section == "region_lists" {
						unique := map[string]any{}
						for _, e := range entries {
							b, _ := json.Marshal(e)
							unique[string(b)] = e
						}
						keys := []string{}
						for k := range unique {
							keys = append(keys, k)
						}
						sort.Strings(keys)
						entries = nil
						for _, k := range keys {
							entries = append(entries, unique[k])
						}
					}
				}
			}
			for k := range object {
				if !allowed[k] {
					return nil, fmt.Errorf("unsupported %s field %s", section, k)
				}
			}
			if len(entries) == 0 {
				return nil, errors.New("policy object requires entries")
			}
			refs := map[string]string{}
			if section == "sla_classes" {
				if v, exists := object["app_probe_class"]; exists {
					name, e := policyString(v)
					if e != nil {
						return nil, e
					}
					refs["appProbeClass"] = name
				}
			}
			out = append(out, Resource{ProfileRefs: refs, Key: "policy_objects/" + section + "/" + name, Name: name, Section: "policy_objects", Kind: "policy-list", Type: spec.kind, Collection: "/dataservice/template/policy/list/" + spec.endpoint, Body: map[string]any{"name": name, "type": spec.kind, "entries": entries}})
		}
	}
	for _, r := range out {
		for _, name := range r.ProfileRefs {
			found := false
			for _, candidate := range out {
				if candidate.Type == "appProbe" && candidate.Name == name {
					found = true
					break
				}
			}
			if !found {
				return nil, errors.New("SLA app_probe_class must reference a declared probe")
			}
		}
	}
	return out, nil
}

func (c *Client) observePolicyList(ctx context.Context, r Resource) (*Observation, error) {
	if e := c.resolvePolicyRefs(ctx, r); e != nil {
		if errors.Is(e, ErrDependencyPending) {
			return nil, nil
		}
		return nil, e
	}
	var list struct {
		Data *[]struct {
			ID   string `json:"listId"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"data"`
	}
	if e := c.Request(ctx, "GET", r.Collection, nil, &list); e != nil {
		return nil, e
	}
	if list.Data == nil {
		return nil, ErrResponse
	}
	id := ""
	for _, item := range *list.Data {
		if item.Name == r.Name {
			if id != "" || !validID.MatchString(item.ID) || !strings.EqualFold(item.Type, r.Type) {
				return nil, ErrResponse
			}
			id = item.ID
		}
	}
	if id == "" {
		return nil, nil
	}
	var body map[string]any
	if e := c.Request(ctx, "GET", r.Collection+"/"+id, nil, &body); e != nil {
		return nil, e
	}
	if body["name"] != r.Name || !strings.EqualFold(fmt.Sprint(body["type"]), r.Type) {
		return nil, ErrResponse
	}
	// Fail closed when the manager cannot establish whether other policies use it.
	count, ok := body["referenceCount"].(float64)
	if !ok || count < 0 {
		return nil, ErrResponse
	}
	return &Observation{ID: id, Body: body, Attached: count > 0}, nil
}
func equalPolicyList(r Resource, body map[string]any) (bool, error) {
	for _, k := range []string{"name", "type", "entries"} {
		if _, ok := body[k]; !ok {
			return false, ErrResponse
		}
	}
	if !strings.EqualFold(fmt.Sprint(body["type"]), r.Type) {
		return false, nil
	}
	a, e := json.Marshal(r.Body)
	if e != nil {
		return false, e
	}
	b, e := json.Marshal(map[string]any{"name": body["name"], "type": r.Type, "entries": body["entries"]})
	return string(a) == string(b), e
}

func (c *Client) resolvePolicyRefs(ctx context.Context, r Resource) error {
	if r.Kind != "policy-list" || len(r.ProfileRefs) == 0 {
		return nil
	}
	name := r.ProfileRefs["appProbeClass"]
	spec := policyLists["app_probe_classes"]
	probe := Resource{Name: name, Type: spec.kind, Kind: "policy-list", Collection: "/dataservice/template/policy/list/" + spec.endpoint}
	o, e := c.observePolicyList(ctx, probe)
	if e != nil {
		return e
	}
	if o == nil {
		return ErrDependencyPending
	}
	entries, ok := r.Body["entries"].([]any)
	if !ok || len(entries) != 1 {
		return ErrResponse
	}
	entry, ok := entries[0].(map[string]any)
	if !ok {
		return ErrResponse
	}
	entry["appProbeClass"] = o.ID
	return nil
}
