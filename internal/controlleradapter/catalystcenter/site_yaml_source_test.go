// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	yaml "go.yaml.in/yaml/v3"
	"testing"
)

// Source authoring stays outside reconciliation. A merge-sequence anchor and a
// caller-bound parent selector must normalize to the same executable geometry.
func resolveYAMLSiteDesignFixture(t *testing.T, raw []byte) []byte {
	t.Helper()
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		t.Fatal("invalid source fixture")
	}
	floor := root["catalyst_center"].(map[string]any)["sites"].(map[string]any)["floors"].([]any)[0].(map[string]any)
	parent := floor["parent_name"].(string)
	var document yaml.Node
	if yaml.Unmarshal(raw, &document) != nil {
		t.Fatal("invalid source document")
	}
	lookup := func(n *yaml.Node, name string) *yaml.Node {
		for i := 0; i < len(n.Content); i += 2 {
			if n.Content[i].Value == name {
				return n.Content[i+1]
			}
		}
		t.Fatal("missing source node")
		return nil
	}
	site := lookup(lookup(document.Content[0], "catalyst_center"), "sites")
	node := lookup(site, "floors").Content[0]
	geometry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Anchor: "floor_geometry"}
	var kept []*yaml.Node
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Value == "width" || key.Value == "length" || key.Value == "height" {
			geometry.Content = append(geometry.Content, key, value)
		} else {
			if key.Value == "parent_name" {
				value.Tag = "!env"
				value.Value = "CVK_SOURCE_PARENT"
				value.Style = yaml.TaggedStyle
			}
			kept = append(kept, key, value)
		}
	}
	if len(geometry.Content) == 0 {
		t.Fatal("fixture geometry absent")
	}
	sequence := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{geometry, {Kind: yaml.AliasNode, Value: geometry.Anchor, Alias: geometry}}}
	node.Content = append([]*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!merge", Value: "<<"}, sequence}, kept...)
	source, err := yaml.Marshal(&document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nacmodel.ParseDocument(source); err == nil {
		t.Fatal("runtime parser accepted unresolved source references")
	}
	resolved, meta, err := nacmodel.MergeModelSourcesWithOptions([][]byte{source}, "model", nacmodel.ModelSourceOptions{ResolveYAML: true, Environment: map[string]string{"CVK_SOURCE_PARENT": parent}})
	if err != nil || !meta.YAMLSourceResolved || meta.EnvironmentBindings != 1 || meta.Resolved || meta.ConfigurationParity {
		t.Fatal("offline source resolution failed", err)
	}
	var got map[string]any
	if json.Unmarshal(resolved, &got) != nil {
		t.Fatal("invalid resolved source")
	}
	// JSON normalizes numeric representations consistently in both snapshots.
	a, _ := json.Marshal(root)
	b, _ := json.Marshal(got)
	if string(a) != string(b) {
		t.Fatal("source preprocessing changed desired model")
	}
	return resolved
}

func TestYAMLSourceSiteDesignManifest(t *testing.T) {
	testPlannedAPManifestFloorThenPlacement(t, "yaml-merged-defaults-image-selected-keyed-recover-private")
}
