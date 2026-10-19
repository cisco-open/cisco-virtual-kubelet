// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package nacmodel

import (
	"encoding/json"
	"errors"
	yamlv3 "go.yaml.in/yaml/v3"
	"path"
	"strings"
	"unicode/utf8"
)

// ResolveTemplateContents maps operator-supplied file assets to canonical
// template identities. The caller supplies a version-bound snapshot; this code
// never reads worker-local paths or retrieves files from URLs.
// NetAsCode prefers project#name over name and normalizes CRLF to LF. Reject
// duplicate stems across extensions instead of depending on filesystem order.
func (i *TemplateIndex) ResolveTemplateContents(files map[string]string) (map[string]string, error) {
	stems := map[string]string{}
	total := 0
	for filename, content := range files {
		if path.Base(filename) != filename || strings.ContainsAny(filename, "\\\x00") || !utf8.ValidString(content) {
			return nil, errors.New("invalid template file asset")
		}
		extension := path.Ext(filename)
		if extension != ".j2" && extension != ".vlt" && extension != ".vtl" {
			return nil, errors.New("unsupported template file extension")
		}
		stem := strings.TrimSuffix(filename, extension)
		if stem == "" {
			return nil, errors.New("empty template file identity")
		}
		if _, exists := stems[stem]; exists {
			return nil, errors.New("duplicate template file stem")
		}
		total += len(content)
		if total > 1<<20 {
			return nil, errors.New("template content exceeds 1 MiB")
		}
		stems[stem] = strings.ReplaceAll(content, "\r\n", "\n")
	}
	result := map[string]string{}
	for key, t := range i.Templates {
		if t.Composite {
			continue
		}
		content, ok := stems[key]
		if !ok {
			content, ok = stems[t.Name]
		}
		if !ok {
			return nil, errors.New("regular template content is missing")
		}
		result[key] = content
	}
	return result, nil
}

// TemplateAssetsKey is an optional JSON filename-to-content map in the SAME
// ConfigMap as the model. Keeping one Kubernetes object makes its resourceVersion
// an atomic revision for both model and files, without new credential/RBAC paths.
const TemplateAssetsKey = "cvk-template-assets.json"

func ParseTemplateAssets(raw []byte) (map[string]string, error) {
	if len(raw) > 1<<20 || !json.Valid(raw) {
		return nil, errors.New("invalid template asset JSON")
	}
	var node yamlv3.Node
	if yamlv3.Unmarshal(raw, &node) != nil || checkNode(&node, 0) != nil || len(node.Content) != 1 || node.Content[0].Kind != yamlv3.MappingNode {
		return nil, errors.New("invalid or duplicate template asset keys")
	}
	object := node.Content[0]
	out := map[string]string{}
	for i := 0; i < len(object.Content); i += 2 {
		value := object.Content[i+1]
		if value.Kind != yamlv3.ScalarNode || value.Tag != "!!str" {
			return nil, errors.New("template assets must contain text values")
		}
		out[object.Content[i].Value] = value.Value
	}
	// Validate every asset even if no template currently references it.
	empty := &TemplateIndex{Templates: map[string]TemplateIdentity{}}
	if _, err := empty.ResolveTemplateContents(out); err != nil {
		return nil, err
	}
	return out, nil
}
