// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

// Selections use display-label -> actual-value mappings. Never send labels in
// place of values or copy controller defaults into the deployment request.
type templateDeploymentSelection struct {
	kind     string
	allowed  map[string]bool
	defaults []string
}

func readTemplateDeploymentSelection(raw any) (*templateDeploymentSelection, error) {
	if raw == nil {
		return nil, nil
	}
	selection, ok := raw.(map[string]any)
	if !ok {
		return nil, errInvalidResponse
	}
	if len(selection) == 0 {
		return nil, nil
	}
	kind, ok := selection["selectionType"].(string)
	if !ok || (kind != "SINGLE_SELECT" && kind != "MULTI_SELECT") {
		return nil, errInvalidResponse
	}
	values, ok := selection["selectionValues"].(map[string]any)
	if !ok || len(values) > 1000 {
		return nil, errInvalidResponse
	}
	// An explicitly cleared choice map still has a selection mode. It allows
	// no selected values, rather than making an optional declaration malformed
	// or silently converting it into an unconstrained free-text parameter.
	result := &templateDeploymentSelection{kind: kind, allowed: map[string]bool{}}
	for _, raw := range values {
		value, ok := raw.(string)
		if !ok {
			return nil, errInvalidResponse
		}
		result.allowed[value] = true
	}
	if raw, present := selection["defaultSelectedValues"]; present && raw != nil {
		defaults, ok := raw.([]any)
		if !ok || len(defaults) > 1000 {
			return nil, errInvalidResponse
		}
		for _, raw := range defaults {
			value, ok := raw.(string)
			if !ok {
				return nil, errInvalidResponse
			}
			result.defaults = append(result.defaults, value)
		}
		if len(result.defaults) > 0 {
			if err := result.validate(result.defaults); err != nil {
				return nil, errInvalidResponse
			}
		}
	}
	return result, nil
}
func (s *templateDeploymentSelection) validate(values []string) error {
	if s == nil {
		return nil
	}
	if s.kind == "SINGLE_SELECT" && len(values) != 1 {
		return errConfigurationTransition
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !s.allowed[value] || seen[value] {
			return errConfigurationTransition
		}
		seen[value] = true
	}
	return nil
}
