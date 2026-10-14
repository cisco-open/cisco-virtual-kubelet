// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

var customServerName = regexp.MustCompile(`^(\*?[^*,\s]+)(\s*,\s*\*?[^*,\s]+)*$`)

func customStrings(raw any, validate func(string) bool) ([]string, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, errors.New("custom application field must be an array")
	}
	seen := map[string]bool{}
	values := []string{}
	for _, item := range items {
		v, ok := item.(string)
		if !ok || !validate(v) {
			return nil, errors.New("invalid custom application list value")
		}
		if !seen[v] {
			seen[v] = true
			values = append(values, v)
		}
	}
	sort.Strings(values)
	return values, nil
}

func customIPv4(value string) bool {
	if prefix, err := netip.ParsePrefix(value); err == nil {
		return prefix.Addr().Is4() && prefix.Bits() >= 24 && prefix.Bits() <= 32
	}
	address, err := netip.ParseAddr(value)
	return err == nil && address.Is4()
}

func compileCustomApplications(catalog *ApplicationCatalog) ([]Resource, error) {
	if catalog == nil {
		return nil, nil
	}
	if len(catalog.CustomApplications) == 0 {
		return nil, errors.New("application_catalog requires custom_applications")
	}
	seen := map[string]bool{}
	out := make([]Resource, 0, len(catalog.CustomApplications))
	for _, input := range catalog.CustomApplications {
		name, ok := input["name"].(string)
		if !ok || name == "" || len(name) > 128 || strings.TrimSpace(name) != name || seen[name] {
			return nil, errors.New("custom applications require unique names")
		}
		seen[name] = true
		body := map[string]any{"appName": name, "L3L4": []any{}}
		attributes := map[string]any{}
		for public, api := range map[string]string{"application_family": "application-family", "application_group": "application-group", "traffic_class": "traffic-class"} {
			if raw, exists := input[public]; exists {
				value, ok := raw.(string)
				if !ok || value == "" {
					return nil, fmt.Errorf("invalid custom application %s", public)
				}
				attributes[api] = value
			}
		}
		if raw, exists := input["business_relevance"]; exists {
			value, ok := raw.(string)
			mapped := map[string]string{"Bronze": "business-irrelevant", "Gold": "business-relevant", "Silver": "default"}[value]
			if !ok || mapped == "" {
				return nil, errors.New("invalid custom application business_relevance")
			}
			attributes["business-relevance"] = mapped
		}
		if len(attributes) > 0 {
			body["attributes"] = attributes
		}
		if raw, exists := input["server_names"]; exists {
			values, err := customStrings(raw, customServerName.MatchString)
			if err != nil {
				return nil, err
			}
			body["serverNames"] = values
		}
		if raw, exists := input["l3l4"]; exists {
			rows, ok := raw.([]any)
			if !ok {
				return nil, errors.New("custom application l3l4 must be an array")
			}
			mapped := []any{}
			for _, item := range rows {
				row, ok := item.(map[string]any)
				if !ok {
					return nil, errors.New("invalid custom application l3l4 row")
				}
				entry := map[string]any{}
				for field, value := range row {
					switch field {
					case "ip_addresses":
						values, err := customStrings(value, customIPv4)
						if err != nil {
							return nil, err
						}
						entry["ipAddresses"] = values
					case "l4_protocol":
						s, ok := value.(string)
						if !ok || (s != "TCP" && s != "UDP" && s != "TCP-UDP") {
							return nil, errors.New("invalid custom application protocol")
						}
						entry["l4Protocol"] = s
					case "ports":
						s, ok := value.(string)
						if !ok || s == "" {
							return nil, errors.New("invalid custom application ports")
						}
						entry["ports"] = s
					default:
						return nil, errors.New("unsupported custom application l3l4 field")
					}
				}
				mapped = append(mapped, entry)
			}
			body["L3L4"] = mapped
		}
		for field := range input {
			switch field {
			case "name", "application_family", "application_group", "business_relevance", "l3l4", "server_names", "traffic_class":
			default:
				return nil, fmt.Errorf("unsupported custom application field %s", field)
			}
		}
		out = append(out, Resource{Key: "application_catalog/custom_applications/" + name, Name: name, Section: "application_catalog", Kind: "custom-app", Type: "custom_application", Collection: "/dataservice/template/policy/customapp/", Body: body})
	}
	return out, nil
}

func equalCustomApplication(r Resource, observed map[string]any) (bool, error) {
	data, ok := observed["data"].(map[string]any)
	if !ok {
		return false, ErrResponse
	}
	if data["appName"] != r.Name {
		return false, ErrResponse
	}
	left := customApplicationCanonical(r.Body)
	right := customApplicationCanonical(data)
	a, err := json.Marshal(left)
	if err != nil {
		return false, err
	}
	b, err := json.Marshal(right)
	return string(a) == string(b), err
}

func customApplicationCanonical(input map[string]any) map[string]any {
	out := map[string]any{"appName": input["appName"]}
	for _, field := range []string{"serverNames", "L3L4", "attributes"} {
		if v, ok := input[field]; ok {
			out[field] = v
		}
	}
	return out
}
