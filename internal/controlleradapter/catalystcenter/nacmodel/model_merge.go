// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package nacmodel

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	yaml "go.yaml.in/yaml/v3"
	"math"
)

const modelMergeExpressionsSHA256 = "f233687b5d6cd244adcb08369a78b7082d3fcb389a7163601c0104975fb5a91f"
const modelMergeVersionsSHA256 = "997aa975cf1dbd46f954e6d6e1e318176654e071e069dfaaa0e472caecaf9494"
const modelMergeReferenceSHA256 = "5f4ebe5f4ff996e136e91bb7e7e1b35f878d686ce95d4b4480b35d21e64e2d94"

const ModelMergeUtilsRevision = "a383caa57652edc7952f3bd596f9a75719480643"

//go:embed upstream/0.5.0/merge.tf
var modelMergeExpressions []byte

//go:embed upstream/0.5.0/versions.tf
var modelMergeVersions []byte

//go:embed upstream/utils-2.0.0/merge.go.txt
var modelMergeReference []byte

//go:embed model_merge_algorithm.go
var modelMergeAlgorithm []byte

//go:embed model_merge.go
var modelMergePolicy []byte

//go:embed document.go
var modelMergeDocumentPolicy []byte

//go:embed source_yaml.go
var modelSourceYAMLPolicy []byte

var ModelMergePolicySHA256 = defaultsDigest(bytes.Join([][]byte{modelMergePolicy, modelMergeAlgorithm, modelMergeReference, modelMergeExpressions, modelMergeVersions, modelMergeDocumentPolicy, modelSourceYAMLPolicy}, []byte{0}))

type ModelMergeProvenance struct {
	YAMLSourceResolved    bool     `json:"yamlSourceResolved"`
	EffectiveInputsSHA256 []string `json:"effectiveInputsSHA256,omitempty"`
	InputSnapshotSHA256   string   `json:"inputSnapshotSHA256"`
	EnvironmentBindings   int      `json:"environmentBindings"`
	Format                string   `json:"format"`
	ModelVersion          string   `json:"modelVersion"`
	UpstreamRevision      string   `json:"upstreamRevision"`
	UtilsVersion          string   `json:"utilsVersion"`
	UtilsRevision         string   `json:"utilsRevision"`
	PolicySHA256          string   `json:"policySHA256"`
	InputsSHA256          []string `json:"inputsSHA256"`
	OrderedInputsSHA256   string   `json:"orderedInputsSHA256"`
	OutputSHA256          string   `json:"outputSHA256"`
	Component             string   `json:"component"`
	Resolved              bool     `json:"resolved"`
	ConfigurationParity   bool     `json:"configurationParity"`
	Limitations           []string `json:"limitations"`
}

// MergeModelSources is offline source construction, not runtime defaulting or
// schema validation. Inputs are ordered by the caller like merge.tf: directories,
// explicit files, then inline model. It never opens a model path or reads env.
// Null entries in newly inserted maps remain intact; null updates to existing
// map keys are ignored, exactly as in the pinned utils merge function.
func MergeModelSources(inputs [][]byte, component string) ([]byte, ModelMergeProvenance, error) {
	return MergeModelSourcesWithOptions(inputs, component, ModelSourceOptions{})
}

func MergeModelSourcesWithOptions(inputs [][]byte, component string, options ModelSourceOptions) ([]byte, ModelMergeProvenance, error) {
	fail := func() ([]byte, ModelMergeProvenance, error) {
		return nil, ModelMergeProvenance{}, errors.New("unsupported controller merge input")
	}
	if Version != NetworkDefaultsVersion || Revision != NetworkDefaultsRevision || defaultsDigest(modelMergeExpressions) != modelMergeExpressionsSHA256 || defaultsDigest(modelMergeVersions) != modelMergeVersionsSHA256 || defaultsDigest(modelMergeReference) != modelMergeReferenceSHA256 {
		return fail()
	}
	if len(inputs) == 0 || len(inputs) > 128 || (component != "all" && component != "model" && component != "defaults") {
		return fail()
	}
	meta := ModelMergeProvenance{Format: Format, ModelVersion: Version, UpstreamRevision: Revision, UtilsVersion: "2.0.0", UtilsRevision: ModelMergeUtilsRevision, PolicySHA256: ModelMergePolicySHA256, Component: component, InputsSHA256: []string{}, Limitations: []string{"Offline merge is not defaults resolution, schema validation or execution parity. Use the existing section exporter and runtime ownership/recovery path.", "Only explicit Catalyst Center/defaults mappings with core scalar tags are admitted. Resolve anchors, YAML merges, custom tags and environment substitution upstream. Input order is significant; duplicate list members are preserved as upstream specifies.", "The module permits utils >= 2.0.0; CVK pins the merge algorithm to 2.0.0, not arbitrary later provider semantics."}}
	if len(options.Environment) > 128 || !options.ResolveYAML && len(options.Environment) > 0 {
		return fail()
	}
	if options.ResolveYAML {
		meta.YAMLSourceResolved = true
		meta.Limitations[1] = "Explicit offline YAML references/tags are resolved with caller-authorized bindings only. Duplicate canonical keys, cycles, invalid tags, nonfinite values and expansion beyond existing bounds refuse; runtime parsing remains strict."
	}
	used := map[string]bool{}
	resolvedBytes := 0
	merged := map[string]any{}
	total := 0
	for _, raw := range inputs {
		total += len(raw)
		if total > 1<<20 {
			return fail()
		}
		var object map[string]any
		if options.ResolveYAML {
			decoded, err := decodeSourceYAML(raw)
			if err != nil {
				return fail()
			}
			budget := 0
			bound, err := resolveSourceBindings(decoded, options.Environment, used, &budget)
			if err != nil {
				return fail()
			}
			object = bound.(map[string]any)
		} else {
			node, err := ParseMappingDocument(raw)
			if err != nil || !validModelMergeNode(node) || node.Decode(&object) != nil {
				return fail()
			}
		}
		if len(object) == 0 {
			return fail()
		}
		canonical, err := json.Marshal(object)
		if err != nil {
			return fail()
		}
		resolvedBytes += len(canonical)
		if resolvedBytes > 1<<20 {
			return fail()
		}
		meta.EffectiveInputsSHA256 = append(meta.EffectiveInputsSHA256, defaultsDigest(canonical))

		for k, v := range object {
			if k != Stripe && k != "defaults" {
				return fail()
			}
			if _, ok := v.(map[string]any); !ok {
				return fail()
			}
		}
		mergeModelMaps(object, merged, true)
		meta.InputsSHA256 = append(meta.InputsSHA256, defaultsDigest(raw))
	}
	stripe, ok := merged[Stripe].(map[string]any)
	if !ok || len(stripe) == 0 {
		return fail()
	}
	if d, ok := merged["defaults"]; ok {
		obj := d.(map[string]any)
		for k := range obj {
			if k != Stripe {
				return fail()
			}
		}
		if v, ok := obj[Stripe]; ok {
			if _, ok := v.(map[string]any); !ok {
				return fail()
			}
		}
	}
	var output any = merged
	switch component {
	case "model":
		output = map[string]any{Stripe: stripe}
	case "defaults":
		d, ok := merged["defaults"].(map[string]any)
		if !ok {
			return fail()
		}
		if _, ok := d[Stripe]; !ok {
			return fail()
		}
		output = map[string]any{"defaults": d}
	}
	raw, err := json.MarshalIndent(output, "", "  ")
	if err != nil || len(raw)+1 > 1<<20 {
		return fail()
	}
	raw = append(raw, '\n')
	digests, _ := json.Marshal(meta.InputsSHA256)
	meta.OrderedInputsSHA256 = defaultsDigest(digests)
	binding, _ := json.Marshal([]any{meta.InputsSHA256, meta.EffectiveInputsSHA256, options.ResolveYAML, ModelMergePolicySHA256})
	meta.InputSnapshotSHA256 = defaultsDigest(binding)
	meta.EnvironmentBindings = len(used)
	meta.OutputSHA256 = defaultsDigest(raw)
	return raw, meta, nil
}

func validModelMergeNode(n *yaml.Node) bool {
	switch n.Kind {
	case yaml.MappingNode:
		if n.Tag != "!!map" {
			return false
		}
	case yaml.SequenceNode:
		if n.Tag != "!!seq" {
			return false
		}
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str", "!!bool", "!!null":
		case "!!int":
			var v int
			if n.Decode(&v) != nil {
				return false
			}
		case "!!float":
			var f float64
			if n.Decode(&f) != nil || math.IsInf(f, 0) || math.IsNaN(f) {
				return false
			}
		default:
			return false
		}
	default:
		return false
	}
	for _, c := range n.Content {
		if !validModelMergeNode(c) {
			return false
		}
	}
	return true
}
