// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package nacmodel

import (
	"bytes"
	"errors"
	yamlv3 "go.yaml.in/yaml/v3"
	"io"
)

// ParseDocument enforces an unambiguous single controller stripe. It does not
// resolve defaults or attest leaf-schema conformance. Errors omit input values.
func parseStrictDocument(raw []byte) (*yamlv3.Node, error) {
	if len(raw) > 1<<20 {
		return nil, errors.New("controller model exceeds 1 MiB")
	}
	var doc yamlv3.Node
	decoder := yamlv3.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&doc); err != nil {
		return nil, errors.New("invalid YAML/JSON controller model")
	}
	var trailing yamlv3.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("expected exactly one controller model document")
	}
	if err := checkNode(&doc, 0); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 {
		return nil, errors.New("expected a controller model object")
	}
	root := doc.Content[0]
	return root, nil
}

// ParseMappingDocument exposes the shared structural checks for an explicitly
// supplied offline snapshot. It grants no controller-stripe/schema validation.
func ParseMappingDocument(raw []byte) (*yamlv3.Node, error) {
	root, err := parseStrictDocument(raw)
	if err != nil {
		return nil, err
	}
	if root.Kind != yamlv3.MappingNode {
		return nil, errors.New("expected a single mapping document")
	}
	return root, nil
}

func ParseDocument(raw []byte) (*yamlv3.Node, error) {
	root, err := parseStrictDocument(raw)
	if err != nil {
		return nil, err
	}
	if root.Kind != yamlv3.MappingNode || len(root.Content) != 2 || root.Content[0].Value != Stripe {
		return nil, errors.New("expected only the catalyst_center root; resolve defaults and select the controller stripe upstream")
	}
	stripe := root.Content[1]
	if stripe.Kind != yamlv3.MappingNode || len(stripe.Content) == 0 {
		return nil, errors.New("catalyst_center must be a non-empty object")
	}
	return stripe, nil
}

// ParseDefaultsDocument shares the exact single-document structural contract
// with model input, returning only the explicitly selected controller stripe.
func ParseDefaultsDocument(raw []byte) (*yamlv3.Node, error) {
	root, err := parseStrictDocument(raw)
	if err != nil {
		return nil, err
	}
	if root.Kind != yamlv3.MappingNode || len(root.Content) != 2 || root.Content[0].Value != "defaults" {
		return nil, errors.New("expected only defaults root")
	}
	defaults := root.Content[1]
	if defaults.Kind != yamlv3.MappingNode || len(defaults.Content) != 2 || defaults.Content[0].Value != Stripe || defaults.Content[1].Kind != yamlv3.MappingNode {
		return nil, errors.New("expected only catalyst_center defaults")
	}
	return defaults.Content[1], nil
}

func checkNode(node *yamlv3.Node, depth int) error {
	if depth > 64 {
		return errors.New("controller model exceeds maximum nesting depth")
	}
	if node.Kind == yamlv3.AliasNode || node.Anchor != "" {
		return errors.New("resolve YAML anchors and aliases upstream")
	}
	if node.Kind == yamlv3.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Tag != "!!str" || key.Kind != yamlv3.ScalarNode {
				return errors.New("controller model keys must be strings; YAML merges are unsupported")
			}
			if seen[key.Value] {
				return errors.New("duplicate controller model key")
			}
			seen[key.Value] = true
		}
	}
	for _, child := range node.Content {
		if err := checkNode(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}
