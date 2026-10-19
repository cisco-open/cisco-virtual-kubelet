// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

type templateDeploymentTarget struct {
	template                 nacmodel.TemplateIdentity
	external                 bool
	redeploy                 string
	copyingConfig, forcePush bool
	parameters               map[string][]string
	memberParameters         map[string]map[string][]string
	comparison               *templateInputComparison
}

// Regular execution uses the common parsed target without member variables.
type regularTemplateTarget = templateDeploymentTarget

func validTemplateRedeploy(value string) bool {
	return value == "NEVER" || value == "ON_CHANGE" || value == "ALWAYS"
}

// Definition identity and redeploy metadata only. Definition body validation
// remains the existing template compiler's responsibility.
func templateDeploymentCatalog(raw []byte) (*nacmodel.TemplateIndex, map[string]string, error) {
	index, err := nacmodel.IndexTemplates(raw)
	if err != nil {
		return nil, nil, err
	}
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return nil, nil, errInvalidResponse
	}
	policies := map[string]string{}
	if section, ok := root["templates"].(map[string]any); ok {
		if projects, ok := section["projects"].([]any); ok {
			for _, entry := range projects {
				project := entry.(map[string]any) // IndexTemplates checked these shapes.
				for _, kind := range []string{"dayn_templates", "onboarding_templates"} {
					definitions, _ := project[kind].([]any)
					for _, entry := range definitions {
						definition := entry.(map[string]any)
						policy := "NEVER"
						if value, present := definition["redeploy_template"]; present {
							var ok bool
							policy, ok = value.(string)
							if !ok || !validTemplateRedeploy(policy) {
								return nil, nil, errInvalidResponse
							}
						}
						policies[project["name"].(string)+"#"+definition["name"].(string)] = policy
					}
				}
			}
		}
	}
	return index, policies, nil
}

// Terraform's list(string) deployment parameters coerce primitive scalars to
// strings. Nested objects, nulls and non-finite values are never CLI parameters.
func templateParameterScalar(value any) (string, error) {
	switch value := value.(type) {
	case string:
		return value, nil
	case bool:
		return strconv.FormatBool(value), nil
	case int:
		return strconv.Itoa(value), nil
	case int64:
		return strconv.FormatInt(value, 10), nil
	case uint64:
		return strconv.FormatUint(value, 10), nil
	case float64:
		// Integral floats above 2^53 may already have lost source precision.
		// Require an exact integer type or authored string instead of silently
		// changing a template default or deployment parameter.
		if math.Abs(value) > 9007199254740991 {
			return "", errInvalidResponse
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", errInvalidResponse
		}
		return string(encoded), nil
	default:
		return "", errInvalidResponse
	}
}
func templateParameterValues(value any) ([]string, error) {
	if list, ok := value.([]any); ok {
		if len(list) > 1000 {
			return nil, errInvalidResponse
		}
		values := make([]string, 0, len(list))
		for _, value := range list {
			scalar, err := templateParameterScalar(value)
			if err != nil {
				return nil, err
			}
			values = append(values, scalar)
		}
		return values, nil
	}
	scalar, err := templateParameterScalar(value)
	if err != nil {
		return nil, err
	}
	return []string{scalar}, nil
}

func parseRegularTemplateTargets(value any, index *nacmodel.TemplateIndex, policies map[string]string) ([]regularTemplateTarget, error) {
	return parseTemplateTargets(value, index, policies, false)
}
func parseCompositeTemplateTargets(value any, index *nacmodel.TemplateIndex, policies map[string]string) ([]compositeTemplateTarget, error) {
	parsed, err := parseTemplateTargets(value, index, policies, true)
	if err != nil {
		return nil, err
	}
	out := make([]compositeTemplateTarget, 0, len(parsed))
	for _, target := range parsed {
		out = append(out, compositeTemplateTarget{template: target.template, redeploy: target.redeploy, copyingConfig: target.copyingConfig, forcePush: target.forcePush, parameters: target.memberParameters})
	}
	return out, nil
}
func parseTemplateTargets(value any, index *nacmodel.TemplateIndex, policies map[string]string, composite bool) ([]templateDeploymentTarget, error) {
	collection := "regular"
	if composite {
		collection = "composite"
	}

	section, ok := value.(map[string]any)
	if !ok || index == nil {
		return nil, errInvalidResponse
	}
	devicePolicy := ""
	for key, value := range section {
		switch key {
		case collection:
		case "redeploy_template":
			devicePolicy, ok = value.(string)
			if !ok || !validTemplateRedeploy(devicePolicy) {
				return nil, errInvalidResponse
			}
		default:
			return nil, errConfigurationTransition // composite is not discarded.
		}
	}
	entries, ok := section[collection].([]any)
	if !ok || len(entries) == 0 || len(entries) > 1000 {
		return nil, errInvalidResponse
	}
	out := make([]regularTemplateTarget, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		model, ok := entry.(map[string]any)
		if !ok {
			return nil, errInvalidResponse
		}
		for key := range model {
			switch key {
			case "name", "project_name", "redeploy_template", "copying_config", "force_push_template", "variables":
			default:
				return nil, errInvalidResponse
			}
		}
		name, ok := model["name"].(string)
		if !ok {
			return nil, errInvalidResponse
		}
		if value, present := model["project_name"]; present {
			project, ok := value.(string)
			if !ok || project == "" || strings.Contains(name, "#") || strings.Contains(project, "#") {
				return nil, errInvalidResponse
			}
			name = project + "#" + name
		}
		identity, external, err := index.ResolveReference(name)
		if err != nil {
			return nil, err
		}
		if external {
			identity.Composite = composite
		}
		if identity.Onboarding || identity.Composite != composite || seen[identity.Key()] {
			return nil, errInvalidResponse
		}
		seen[identity.Key()] = true
		target := regularTemplateTarget{template: identity, external: external, redeploy: "NEVER", copyingConfig: true, forcePush: true, parameters: map[string][]string{}}
		if policy, present := policies[identity.Key()]; present {
			if !validTemplateRedeploy(policy) {
				return nil, errInvalidResponse
			}
			target.redeploy = policy
		}
		if devicePolicy != "" {
			target.redeploy = devicePolicy
		}
		if value, present := model["redeploy_template"]; present {
			policy, ok := value.(string)
			if !ok || !validTemplateRedeploy(policy) {
				return nil, errInvalidResponse
			}
			target.redeploy = policy
		}
		for field, destination := range map[string]*bool{"copying_config": &target.copyingConfig, "force_push_template": &target.forcePush} {
			if value, present := model[field]; present {
				flag, ok := value.(bool)
				if !ok {
					return nil, errInvalidResponse
				}
				*destination = flag
			}
		}
		if value, present := model["variables"]; present {
			list, ok := value.([]any)
			if !ok || len(list) > 1000 {
				return nil, errInvalidResponse
			}
			for _, entry := range list {
				variable, ok := entry.(map[string]any)
				expectedFields := 2
				if composite {
					expectedFields = 3
				}
				if !ok || len(variable) != expectedFields {
					return nil, errInvalidResponse
				}
				name, ok := variable["name"].(string)
				if !ok || name == "" || len(name) > 256 || strings.TrimSpace(name) != name || strings.ContainsAny(name, "\x00\r\n") {
					return nil, errInvalidResponse
				}
				parameters := target.parameters
				if composite {
					member, ok := variable["template_name"].(string)
					if !ok || !validProfileName(member) || strings.Contains(member, "#") {
						return nil, errInvalidResponse
					}
					if len(identity.Members) > 0 {
						found := false
						for _, key := range identity.Members {
							_, memberName, _ := strings.Cut(key, "#")
							found = found || memberName == member
						}
						if !found {
							return nil, errInvalidResponse
						}
					}
					if target.memberParameters == nil {
						target.memberParameters = map[string]map[string][]string{}
					}
					if target.memberParameters[member] == nil {
						target.memberParameters[member] = map[string][]string{}
					}
					parameters = target.memberParameters[member]
				}
				if _, exists := parameters[name]; exists {
					return nil, errInvalidResponse
				}
				value, present := variable["value"]
				if !present {
					return nil, errInvalidResponse
				}
				values, err := templateParameterValues(value)
				if err != nil {
					return nil, err
				}
				parameters[name] = values
			}
		}
		out = append(out, target)
	}
	return out, nil
}

// Extract deployment intent before reusing wired inventory validation. The
// caller must execute every returned target; this is not a fallback that makes
// unsupported day-N intent disappear from the existing public compiler.
type inventoryTemplateTargets struct {
	regular   []regularTemplateTarget
	composite []compositeTemplateTarget
}

func parseRegularTemplateInventory(raw, templates []byte) (inventoryIntent, map[string]string, map[string][]regularTemplateTarget, error) {
	intent, states, all, err := parseTemplateInventory(raw, templates, false)
	if err != nil {
		return inventoryIntent{}, nil, nil, err
	}
	targets := map[string][]regularTemplateTarget{}
	for name, target := range all {
		targets[name] = target.regular
	}
	return intent, states, targets, nil
}
func parseTemplateInventory(raw, templates []byte, allowComposite bool) (inventoryIntent, map[string]string, map[string]inventoryTemplateTargets, error) {
	var zero inventoryIntent
	index, policies, err := templateDeploymentCatalog(templates)
	if err != nil {
		return zero, nil, nil, err
	}
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return zero, nil, nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return zero, nil, nil, errInvalidResponse
	}
	inventory, ok := root["inventory"].(map[string]any)
	if !ok {
		return zero, nil, nil, errInvalidResponse
	}
	devices, ok := inventory["devices"].([]any)
	if !ok || len(devices) > 10000 {
		return zero, nil, nil, errInvalidResponse
	}
	targets := map[string]inventoryTemplateTargets{}
	for _, entry := range devices {
		device, ok := entry.(map[string]any)
		if !ok {
			return zero, nil, nil, errInvalidResponse
		}
		value, present := device["dayn_templates"]
		if !present {
			continue
		}
		name, ok := device["name"].(string)
		if !ok || name == "" {
			return zero, nil, nil, errInvalidResponse
		}
		state, _ := device["state"].(string)
		if state != "PROVISION" && state != "REPROVISION" {
			return zero, nil, nil, errConfigurationTransition
		}
		if _, duplicate := targets[name]; duplicate {
			return zero, nil, nil, errInvalidResponse
		}
		section, ok := value.(map[string]any)
		if !ok {
			return zero, nil, nil, errInvalidResponse
		}
		for key := range section {
			if key != "regular" && key != "composite" && key != "redeploy_template" {
				return zero, nil, nil, errInvalidResponse
			}
		}
		desired := inventoryTemplateTargets{}
		for _, kind := range []string{"regular", "composite"} {
			entries, present := section[kind]
			if !present {
				continue
			}
			if list, ok := entries.([]any); ok && len(list) == 0 && (kind == "regular" || allowComposite) {
				continue
			}
			projection := map[string]any{kind: entries}
			if policy, present := section["redeploy_template"]; present {
				projection["redeploy_template"] = policy
			}
			if kind == "regular" {
				desired.regular, err = parseRegularTemplateTargets(projection, index, policies)
			} else {
				if !allowComposite {
					return zero, nil, nil, errConfigurationTransition
				}
				desired.composite, err = parseCompositeTemplateTargets(projection, index, policies)
			}
			if err != nil {
				return zero, nil, nil, err
			}
		}
		if len(desired.regular)+len(desired.composite) == 0 {
			return zero, nil, nil, errInvalidResponse
		}
		seen := map[string]bool{}
		for _, target := range desired.regular {
			seen[target.template.Key()] = true
		}
		for _, target := range desired.composite {
			if seen[target.template.Key()] {
				return zero, nil, nil, errInvalidResponse
			}
		}
		targets[name] = desired
		delete(device, "dayn_templates")
	}
	if len(targets) == 0 {
		return zero, nil, nil, errInvalidResponse
	}
	projection, err := json.Marshal(map[string]any{"catalyst_center": root})
	if err != nil {
		return zero, nil, nil, errInvalidResponse
	}
	common, states, err := parseWiredInventoryIntent(projection)
	if err != nil {
		return zero, nil, nil, err
	}
	return common, states, targets, nil
}
