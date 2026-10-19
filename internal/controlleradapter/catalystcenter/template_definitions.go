// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"errors"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"slices"
)

type desiredTemplate struct {
	identity nacmodel.TemplateIdentity
	tags     []string
	payload  map[string]any
}
type templateIntent struct {
	projects    []templateProject
	tags        []desiredTag
	definitions []desiredTemplate
}

// Compile the pinned model's template fields into the native API shape. File
// content is supplied separately from the same immutable source observation.
// No template body or variable value may enter status or an error message.
func parseTemplateIntent(raw []byte, files map[string]string) (templateIntent, error) {
	var result templateIntent
	index, err := nacmodel.IndexTemplates(raw)
	if err != nil {
		return result, err
	}
	contents, err := index.ResolveTemplateContents(files)
	if err != nil {
		return result, err
	}
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return result, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return result, errors.New("templates require a section-specific model")
	}
	section, ok := root["templates"].(map[string]any)
	if !ok {
		return result, errors.New("unsupported template section field")
	}
	for key := range section {
		if key != "projects" && key != "tags" && key != "software_version" {
			return result, errors.New("unsupported template section field")
		}
	}
	// The pinned module uses try(template.software_version,
	// templates.software_version, null), not coalesce: an explicit child null
	// suppresses inheritance. Null leaves this optional native field unmanaged;
	// an explicit empty string requests an unconstrained template.
	if value := section["software_version"]; value != nil {
		if _, ok := value.(string); !ok {
			return result, errors.New("invalid template software version fallback")
		}
	}
	if value, present := section["tags"]; present {
		result.tags, err = parseTemplateTags(value)
		if err != nil {
			return result, err
		}
	}
	projects := []any{}
	if value, present := section["projects"]; present {
		var ok bool
		projects, ok = value.([]any)
		if !ok {
			return result, errors.New("invalid template projects")
		}
	}
	if len(projects) == 0 && len(result.tags) == 0 {
		_, projectsDeclared := section["projects"]
		_, tagsDeclared := section["tags"]
		if !projectsDeclared && !tagsDeclared && len(section) != 0 {
			return result, errors.New("empty template intent")
		}
	}
	projected := []any{}
	for _, entry := range projects {
		project, ok := entry.(map[string]any)
		if !ok {
			return result, errors.New("invalid project")
		}
		name, _ := project["name"].(string)
		projection := map[string]any{}
		for key, value := range project {
			switch key {
			case "name", "description":
				projection[key] = value
			case "onboarding_templates", "dayn_templates":
			default:
				return result, errors.New("unknown template project field")
			}
		}
		projected = append(projected, projection)
		for _, collection := range []string{"onboarding_templates", "dayn_templates"} {
			entries, _ := project[collection].([]any)
			for _, entry := range entries {
				object := entry.(map[string]any)
				templateName := object["name"].(string)
				identity := index.Templates[name+"#"+templateName]
				if len(identity.Key()) > 512 {
					return result, errors.New("template identity exceeds status bound")
				}
				payload := map[string]any{"name": templateName, "composite": identity.Composite}
				if fallback := section["software_version"]; fallback != nil {
					payload["softwareVersion"] = fallback
				}
				tags := []string{}
				for key, value := range object {
					switch key {
					case "name", "composite", "containing_templates":
					case "redeploy_template":
						// Deployment policy is consumed from this same model by
						// the inventory compiler, never sent as template content.
						policy, ok := value.(string)
						if !ok || !validTemplateRedeploy(policy) {
							return result, errors.New("invalid template redeploy policy")
						}
					case "software_version":
						if value == nil {
							delete(payload, "softwareVersion")
							continue
						}
						version, ok := value.(string)
						if !ok {
							return result, errors.New("invalid template software version")
						}
						payload["softwareVersion"] = version
					case "description", "language", "software_type":
						if key == "description" && value == nil {
							continue
						}
						v, ok := value.(string)
						if !ok {
							return result, errors.New("invalid template string attribute")
						}
						apiKey := map[string]string{"description": "description", "language": "language", "software_type": "softwareType"}[key]
						payload[apiKey] = v
					case "device_types":
						list, ok := value.([]any)
						if !ok || len(list) == 0 {
							return result, errors.New("template device types must be explicit")
						}
						types := []any{}
						for _, v := range list {
							m, ok := v.(map[string]any)
							if !ok {
								return result, errors.New("invalid template device type")
							}
							translated, err := templateStringFields(m, map[string]string{"product_family": "productFamily", "product_series": "productSeries", "product_type": "productType"}, "product_series", "product_type")
							if err != nil {
								return result, err
							}
							if translated["productFamily"] == nil || translated["productFamily"] == "" {
								return result, errors.New("template device family is required")
							}
							types = append(types, translated)
						}
						payload["deviceTypes"] = types
					case "tags":
						if value == nil {
							continue
						}
						list, ok := value.([]any)
						if !ok || len(list) > 1000 {
							return result, errors.New("invalid template tag references")
						}
						seen := map[string]bool{}
						for _, v := range list {
							name, ok := v.(string)
							if !ok || !validTemplateTagName(name) || seen[name] {
								return result, errors.New("invalid or duplicate template tag reference")
							}
							seen[name] = true
							tags = append(tags, name)
						}
					case "variables":
						variables, err := parseTemplateVariables(value)
						if err != nil {
							return result, err
						}
						payload["templateParams"] = variables
					default:
						return result, errors.New("unsupported template definition field")
					}
				}
				if payload["language"] != "JINJA" && payload["language"] != "VELOCITY" {
					return result, errors.New("template language must be resolved")
				}
				if payload["softwareType"] == nil || payload["softwareType"] == "" || payload["deviceTypes"] == nil {
					return result, errors.New("template software and device types are required")
				}
				if !identity.Composite {
					payload["templateContent"] = contents[identity.Key()]
				}
				if identity.Composite {
					if params, exists := payload["templateParams"]; exists && len(params.([]any)) > 0 {
						return result, errors.New("composite variables belong to member templates")
					}
					delete(payload, "templateParams")
				} else if _, present := payload["templateParams"]; !present {
					payload["templateParams"] = []any{}
				}
				result.definitions = append(result.definitions, desiredTemplate{identity: identity, payload: payload, tags: tags})
			}
		}
	}
	projectedRaw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"templates": map[string]any{"projects": projected}}})
	if len(projected) > 0 {
		result.projects, err = parseTemplateProjects(projectedRaw)
	}
	return result, err
}
func templateStringFields(object map[string]any, mapping map[string]string, nullable ...string) (map[string]any, error) {
	result := map[string]any{}
	for key, value := range object {
		apiKey, ok := mapping[key]
		if !ok {
			return nil, errors.New("unsupported template attribute")
		}
		if value == nil && slices.Contains(nullable, key) {
			continue
		}
		v, ok := value.(string)
		if !ok {
			return nil, errors.New("template string attribute has invalid type")
		}
		result[apiKey] = v
	}
	return result, nil
}
func parseTemplateVariables(value any) ([]any, error) {
	list, ok := value.([]any)
	if !ok || len(list) > 1000 {
		return nil, errors.New("invalid template variable list")
	}
	out := []any{}
	seen := map[string]bool{}
	text := map[string]string{"name": "parameterName", "binding": "binding", "data_type": "dataType", "default_value": "defaultValue", "additional_info": "description", "field_name": "displayName", "hint_text": "instructionText"}
	flags := map[string]string{"not_param": "notParam", "param_array": "paramArray", "required": "required"}
	for _, v := range list {
		object, ok := v.(map[string]any)
		if !ok {
			return nil, errors.New("invalid template variable")
		}
		translated := map[string]any{}
		selection := map[string]any{}
		for key, value := range object {
			if apiKey, ok := text[key]; ok {
				if value == nil && key != "name" && key != "data_type" {
					continue
				}
				if key == "default_value" {
					s, err := templateParameterScalar(value)
					if err != nil {
						return nil, errors.New("invalid template variable default")
					}
					translated[apiKey] = s
					continue
				}
				s, ok := value.(string)
				if !ok {
					return nil, errors.New("invalid template variable string")
				}
				translated[apiKey] = s
				continue
			}
			if apiKey, ok := flags[key]; ok {
				if value == nil {
					continue
				}
				b, ok := value.(bool)
				if !ok {
					return nil, errors.New("invalid template variable boolean")
				}
				translated[apiKey] = b
				continue
			}
			switch key {
			case "selection_type":
				if value == nil {
					continue
				}
				s, ok := value.(string)
				if !ok || s != "SINGLE_SELECT" && s != "MULTI_SELECT" {
					return nil, errors.New("invalid selection type")
				}
				selection["selectionType"] = s
			case "data_values":
				if value == nil {
					continue
				}
				m, ok := value.(map[string]any)
				if !ok {
					return nil, errors.New("invalid selection values")
				}
				values := map[string]any{}
				for name, v := range m {
					text, err := templateParameterScalar(v)
					if err != nil {
						return nil, errors.New("invalid selection value")
					}
					values[name] = text
				}
				selection["selectionValues"] = values
			default:
				return nil, errors.New("unsupported template variable field")
			}
		}
		name, ok := translated["parameterName"].(string)
		if !ok || name == "" || seen[name] {
			return nil, errors.New("template variable names must be unique")
		}
		seen[name] = true
		kind, ok := translated["dataType"].(string)
		if !ok || !validTemplateParameterDataType(kind) {
			return nil, errors.New("invalid or unresolved template variable data type")
		}
		if len(selection) > 0 {
			translated["selection"] = selection
		}
		out = append(out, translated)
	}
	return out, nil
}
