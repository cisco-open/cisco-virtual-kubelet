// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"math/big"
	"net"
	"net/netip"
	"strings"
)

// Validate supplied deployment variables against committed declarations without
// rendering content or copying defaults/bound values into the request. Errors
// never include a parameter name or value. Appliance-side binding resolution,
// non-integer range constraints and platform compatibility remain separate.
func validateTemplateDeploymentParameters(body map[string]any, parameters map[string][]string) error {
	raw, present := body["templateParams"]
	if !present || raw == nil {
		if len(parameters) != 0 {
			return errConfigurationTransition
		}
		return nil
	}
	declarations, ok := raw.([]any)
	if !ok || len(declarations) > 1000 {
		return errInvalidResponse
	}
	seen := map[string]bool{}
	for _, entry := range declarations {
		declaration, ok := entry.(map[string]any)
		if !ok {
			return errInvalidResponse
		}
		name, ok := declaration["parameterName"].(string)
		if !ok || name == "" || strings.TrimSpace(name) != name || seen[name] {
			return errInvalidResponse
		}
		seen[name] = true
		kind, ok := declaration["dataType"].(string)
		if !ok {
			return errInvalidResponse
		}
		if !validTemplateParameterDataType(kind) {
			return errConfigurationTransition
		}
		flags := map[string]bool{}
		for _, key := range []string{"required", "notParam", "paramArray"} {
			if value, present := declaration[key]; present && value != nil {
				flag, ok := value.(bool)
				if !ok {
					return errInvalidResponse
				}
				flags[key] = flag
			}
		}
		binding, defaultValue := "", ""
		for key, destination := range map[string]*string{"binding": &binding, "defaultValue": &defaultValue} {
			if value, present := declaration[key]; present && value != nil {
				text, ok := value.(string)
				if !ok {
					return errInvalidResponse
				}
				*destination = text
			}
		}
		ranges, err := readTemplateIntegerRanges(kind, declaration["range"])
		if err != nil {
			return err
		}
		selection, err := readTemplateDeploymentSelection(declaration["selection"])
		if err != nil {
			return err
		}
		values, supplied := parameters[name]
		if flags["notParam"] || kind == "SECTIONDIVIDER" {
			if supplied {
				return errConfigurationTransition
			}
			continue
		}
		if !supplied {
			if flags["required"] && binding == "" && defaultValue == "" && (selection == nil || len(selection.defaults) == 0) {
				return errConfigurationTransition
			}
			continue
		}
		multiSelect := selection != nil && selection.kind == "MULTI_SELECT"
		if values == nil || (!flags["paramArray"] && !multiSelect && len(values) != 1) || flags["required"] && len(values) == 0 {
			return errConfigurationTransition
		}
		if err := selection.validate(values); err != nil {
			return err
		}
		for _, value := range values {
			if flags["required"] && value == "" {
				return errConfigurationTransition
			}
			switch kind {
			case "INTEGER":
				if err := validateTemplateIntegerRange(ranges, value); err != nil {
					return err
				}
				if strings.TrimSpace(value) != value || strings.ContainsAny(value, "._ ") {
					return errConfigurationTransition
				}
				if _, ok := new(big.Int).SetString(value, 10); !ok {
					return errConfigurationTransition
				}
			case "IPADDRESS":
				ip, err := netip.ParseAddr(value)
				if err != nil || ip.Zone() != "" {
					return errConfigurationTransition
				}
			case "MACADDRESS":
				mac, err := net.ParseMAC(value)
				if err != nil || len(mac) != 6 {
					return errConfigurationTransition
				}
			}
		}
	}
	for name := range parameters {
		if !seen[name] {
			return errConfigurationTransition
		}
	}
	return nil
}
