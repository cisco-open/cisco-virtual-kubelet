// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package nacmodel

import (
	"bytes"
	_ "embed"
	yamlv3 "go.yaml.in/yaml/v3"
	"io"
)

//go:embed defaults_document.go
var defaultsDocumentPolicy []byte

// All scoped exporters share the same size/depth, duplicate-key, alias and
// single-document rules. A defaults map is never accepted as a model stripe.
func readDefaultsStripe(raw []byte, restricted bool) (map[string]any, error) {
	if len(raw) > 1<<20 {
		return nil, errNetworkDefaults
	}
	d := yamlv3.NewDecoder(bytes.NewReader(raw))
	var doc, extra yamlv3.Node
	if d.Decode(&doc) != nil || len(doc.Content) != 1 || d.Decode(&extra) != io.EOF {
		return nil, errNetworkDefaults
	}
	wrapper := &yamlv3.Node{Kind: yamlv3.MappingNode, Tag: "!!map", Content: []*yamlv3.Node{{Kind: yamlv3.ScalarNode, Tag: "!!str", Value: Stripe}, doc.Content[0]}}
	encoded, err := yamlv3.Marshal(wrapper)
	if err != nil {
		return nil, errNetworkDefaults
	}
	checked, err := ParseDocument(encoded)
	if err != nil {
		return nil, errNetworkDefaults
	}
	var root map[string]any
	if checked.Decode(&root) != nil || len(root) != 1 {
		return nil, errNetworkDefaults
	}
	object, ok := root["defaults"].(map[string]any)
	if !ok {
		return nil, errNetworkDefaults
	}
	if restricted {
		for key := range object {
			if key != Stripe {
				return nil, errNetworkDefaults
			}
		}
	}
	stripe, ok := object[Stripe].(map[string]any)
	if !ok {
		return nil, errNetworkDefaults
	}
	return stripe, nil
}
