// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package nacmodel

import (
	"errors"
	"strings"
)

// TemplateIdentity is independent of appliance UUIDs and version UUIDs. A
// canonical identity always includes its project, even when the upstream
// resource key can use an unambiguous bare name.
type TemplateIdentity struct {
	Project    string
	Name       string
	Onboarding bool
	Composite  bool
	Members    []string
}

func (t TemplateIdentity) Key() string { return t.Project + "#" + t.Name }

type TemplateIndex struct {
	Templates map[string]TemplateIdentity
	byName    map[string][]string
}

func validTemplateName(name string) bool {
	return name != "" && strings.TrimSpace(name) == name && len(name) <= 256 && !strings.ContainsAny(name, "#\x00\r\n")
}

// IndexTemplates validates identity and composite references only. Payload
// schema, defaults, external file content and deployed versions are separate
// contracts; this function must never be advertised as full model validation.
func IndexTemplates(raw []byte) (*TemplateIndex, error) {
	node, err := ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return nil, errors.New("invalid template model")
	}
	index := &TemplateIndex{Templates: map[string]TemplateIdentity{}, byName: map[string][]string{}}
	value, present := root["templates"]
	if !present {
		return index, nil
	}
	section, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("templates must be an object")
	}
	value, present = section["projects"]
	if !present {
		return index, nil
	}
	projects, ok := value.([]any)
	if !ok || len(projects) > 1000 {
		return nil, errors.New("invalid template projects")
	}
	seen := map[string]bool{}
	for _, entry := range projects {
		p, ok := entry.(map[string]any)
		if !ok {
			return nil, errors.New("invalid template project")
		}
		project, ok := p["name"].(string)
		if !ok || !validTemplateName(project) || seen[project] {
			return nil, errors.New("invalid or duplicate project identity")
		}
		seen[project] = true
		for _, collection := range []string{"onboarding_templates", "dayn_templates"} {
			value, present := p[collection]
			if !present {
				continue
			}
			list, ok := value.([]any)
			if !ok || len(list) > 10000 {
				return nil, errors.New("invalid template collection")
			}
			for _, entry := range list {
				object, ok := entry.(map[string]any)
				if !ok {
					return nil, errors.New("invalid template definition")
				}
				name, ok := object["name"].(string)
				if !ok || !validTemplateName(name) {
					return nil, errors.New("invalid template identity")
				}
				t := TemplateIdentity{Project: project, Name: name, Onboarding: collection == "onboarding_templates"}
				if value, present := object["composite"]; present {
					v, ok := value.(bool)
					if !ok {
						return nil, errors.New("invalid composite flag")
					}
					t.Composite = v
				}
				if t.Composite && t.Onboarding {
					return nil, errors.New("onboarding composites are unsupported by the pinned module")
				}
				if value, present := object["containing_templates"]; present {
					list, ok := value.([]any)
					if !ok || !t.Composite || len(list) == 0 {
						return nil, errors.New("invalid composite members")
					}
					for _, v := range list {
						s, ok := v.(string)
						if !ok || !validTemplateName(s) {
							return nil, errors.New("composite members require bare template names")
						}
						t.Members = append(t.Members, s)
					}
				}
				if t.Composite && len(t.Members) == 0 {
					return nil, errors.New("composite members are required")
				}
				if _, exists := index.Templates[t.Key()]; exists {
					return nil, errors.New("template identity appears more than once in its project")
				}
				if len(index.Templates) >= 10000 {
					return nil, errors.New("template identity count exceeds bound")
				}
				index.Templates[t.Key()] = t
				index.byName[name] = append(index.byName[name], t.Key())
			}
		}
	}
	// Upstream resolves a globally unique member by bare name. Ambiguous names
	// resolve in the composite's project. Only regular members are supported.
	for key, t := range index.Templates {
		seen := map[string]bool{}
		for i, name := range t.Members {
			matches := index.byName[name]
			resolved := t.Project + "#" + name
			if len(matches) == 1 {
				resolved = matches[0]
			}
			member, ok := index.Templates[resolved]
			if !ok || member.Composite || seen[resolved] {
				return nil, errors.New("composite member is missing, composite or repeated")
			}
			seen[resolved] = true
			t.Members[i] = resolved
		}
		index.Templates[key] = t
	}
	return index, nil
}

// ResolveReference accepts an external reference only when project-qualified,
// matching the pinned module's opt-in convention for pre-existing templates.
// Callers must resolve external metadata and committed versions before use.
func (i *TemplateIndex) ResolveReference(reference string) (TemplateIdentity, bool, error) {
	if strings.Contains(reference, "#") {
		parts := strings.Split(reference, "#")
		if len(parts) != 2 || !validTemplateName(parts[0]) || !validTemplateName(parts[1]) {
			return TemplateIdentity{}, false, errors.New("invalid qualified template reference")
		}
		if t, ok := i.Templates[reference]; ok {
			return t, false, nil
		}
		return TemplateIdentity{Project: parts[0], Name: parts[1]}, true, nil
	}
	if !validTemplateName(reference) {
		return TemplateIdentity{}, false, errors.New("invalid template reference")
	}
	keys := i.byName[reference]
	if len(keys) != 1 {
		return TemplateIdentity{}, false, errors.New("bare template reference is missing or ambiguous")
	}
	return i.Templates[keys[0]], false, nil
}
