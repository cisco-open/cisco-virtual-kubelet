// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package nacmodel

import (
	"bytes"
	"errors"
	"fmt"
	yaml "go.yaml.in/yaml/v3"
	"io"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Immutable utils 2.0.0 reference files used by the independent conformance
// runner. Production continues to use CVK's existing YAML decoder.
const sourceYAMLReferenceSHA256 = "94e5d3d1c592a7c222348cc7eb212fc992ef3692407de1fd8649839e3f43fa07"
const sourceTagReferenceSHA256 = "1b49ee1c31552ba89dc7f25aa52d0416ed0f02a097ee1d90acf8b32aad4e8ed1"

// Source resolution is explicit offline preprocessing. Environment contains only
// caller-authorized bindings; this package never reads the process environment.
type ModelSourceOptions struct {
	ResolveYAML bool
	Environment map[string]string
}

var errSourceYAML = errors.New("unsupported offline controller YAML source")

type sourceYAMLDecoder struct {
	active       map[*yaml.Node]bool
	nodes, bytes int
	raw          string
	positions    [][]int
	nonspecific  map[*yaml.Node]bool
}

func decodeSourceYAML(raw []byte) (map[string]any, error) {
	if len(raw) > 1<<20 {
		return nil, errSourceYAML
	}
	var document, trailing yaml.Node
	d := yaml.NewDecoder(bytes.NewReader(raw))
	if d.Decode(&document) != nil || d.Decode(&trailing) != io.EOF || len(document.Content) != 1 {
		return nil, errSourceYAML
	}
	decoder := sourceYAMLDecoder{active: map[*yaml.Node]bool{}, raw: string(raw), nonspecific: map[*yaml.Node]bool{}}
	offset := 0
	for _, line := range strings.SplitAfter(string(raw), "\n") {
		positions := []int{}
		for pos := range line {
			positions = append(positions, offset+pos)
		}
		positions = append(positions, offset+len(line))
		decoder.positions = append(decoder.positions, positions)
		offset += len(line)
	}
	v, err := decoder.value(document.Content[0], 0, false)
	m, ok := v.(map[string]any)
	if err != nil || !ok {
		return nil, errSourceYAML
	}
	return m, nil
}
func (d *sourceYAMLDecoder) value(n *yaml.Node, depth int, flow bool) (any, error) {
	if n == nil || depth > 64 || d.active[n] || d.nodes >= 100000 {
		return nil, errSourceYAML
	}
	d.nodes++
	d.bytes += len(n.Value) + len(n.Tag)
	if d.bytes > 1<<20 {
		return nil, errSourceYAML
	}
	d.active[n] = true
	defer delete(d.active, n)
	if n.Kind == yaml.AliasNode {
		return d.value(n.Alias, depth+1, flow)
	}
	flow = flow || n.Style&yaml.FlowStyle != 0
	if n.Kind == yaml.ScalarNode && d.nonSpecific(n) {
		return "! " + n.Value, nil
	}
	switch n.Kind {
	case yaml.MappingNode:
		if n.Tag != "!!map" {
			return nil, errSourceYAML
		}
		out := map[string]any{}
		explicit := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]
			d.nodes++
			d.bytes += len(key.Value) + len(key.Tag)
			if d.nodes > 100000 || d.bytes > 1<<20 {
				return nil, errSourceYAML
			}
			if key.Tag == "!!merge" && key.Value == "<<" {
				v, err := d.value(n.Content[i+1], depth+1, flow)
				if err != nil {
					return nil, err
				}
				merge := func(m map[string]any) {
					for k, v := range m {
						if _, exists := out[k]; !exists {
							out[k] = v
						}
					}
				}
				switch v := v.(type) {
				case map[string]any:
					merge(v)
				case []any:
					for _, v := range v {
						if m, ok := v.(map[string]any); ok {
							merge(m)
						}
					}
				default:
					return nil, errSourceYAML
				}
				continue
			}
			if key.Kind != yaml.ScalarNode {
				return nil, errSourceYAML
			}
			var k any
			var err error
			if strings.HasPrefix(key.Tag, "!") && !strings.HasPrefix(key.Tag, "!!") || d.nonSpecific(key) {
				k = key.Value
			} else {
				k, err = sourceYAMLScalar(key)
			}
			if err != nil {
				return nil, err
			}
			name, ok := k.(string)
			if !ok {
				name = fmt.Sprint(k)
			}
			if explicit[name] {
				return nil, errSourceYAML
			}
			explicit[name] = true
			v, err := d.value(n.Content[i+1], depth+1, flow)
			if err != nil {
				return nil, err
			}
			out[name] = v
		}
		return out, nil
	case yaml.SequenceNode:
		if n.Tag != "!!seq" {
			return nil, errSourceYAML
		}
		values := make([]any, 0, len(n.Content))
		for _, child := range n.Content {
			v, err := d.value(child, depth+1, flow)
			if err != nil {
				return nil, err
			}
			values = append(values, v)
		}
		return values, nil
	case yaml.ScalarNode:
		// The pinned parser rejects this explicit unquoted flow timestamp form.
		if flow && n.Tag == "!!timestamp" && n.Style&yaml.TaggedStyle != 0 && n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) == 0 && strings.Contains(n.Value, ":") {
			return nil, errSourceYAML
		}
		return sourceYAMLScalar(n)
	default:
		return nil, errSourceYAML
	}
}

// yaml.v3 resolves a non-specific ! scalar tag before exposing its node. Keep
// the pinned provider's literal-tag behavior using source token positions. Cache
// the classification so aliases cannot repeatedly scan a long whitespace run.
func (d *sourceYAMLDecoder) nonSpecific(n *yaml.Node) bool {
	if value, ok := d.nonspecific[n]; ok {
		return value
	}
	d.nonspecific[n] = false
	if n.Line < 1 || n.Line > len(d.positions) || n.Column < 1 || n.Column > len(d.positions[n.Line-1]) {
		return false
	}
	raw := d.raw[d.positions[n.Line-1][n.Column-1]:]
	if strings.HasPrefix(raw, "&") {
		i := strings.IndexFunc(raw, unicode.IsSpace)
		if i < 0 {
			return false
		}
		raw = strings.TrimLeftFunc(raw[i:], unicode.IsSpace)
	}
	if !strings.HasPrefix(raw, "!") || len(raw) < 2 {
		return false
	}
	r, _ := utf8.DecodeRuneInString(raw[1:])
	value := unicode.IsSpace(r)
	d.nonspecific[n] = value
	return value
}

func sourceYAMLScalar(n *yaml.Node) (any, error) {
	switch n.Tag {
	case "!!str":
		// utils converts an explicitly tagged plain numeric value before !!str.
		if n.Style&yaml.TaggedStyle != 0 && n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) == 0 {
			var scalar any
			if yaml.Unmarshal([]byte(n.Value), &scalar) == nil {
				switch v := scalar.(type) {
				case int, uint64, float64, bool, nil:
					return fmt.Sprint(v), nil
				}
			}
		}
		return n.Value, nil
	case "!!null":
		return nil, nil
	case "!!timestamp", "!!binary":
		return n.Value, nil
	case "!!bool":
		var v bool
		if n.Decode(&v) != nil {
			return nil, errSourceYAML
		}
		return v, nil
	case "!!int":
		var v int
		if n.Decode(&v) != nil {
			return nil, errSourceYAML
		}
		return v, nil
	case "!!float":
		var v float64
		if n.Decode(&v) != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, errSourceYAML
		}
		return v, nil
	default:
		if !strings.HasPrefix(n.Tag, "!") || strings.HasPrefix(n.Tag, "!!") || strings.HasPrefix(n.Tag, "tag:") {
			return nil, errSourceYAML
		}
		return n.Tag + " " + n.Value, nil
	}
}

// The pinned provider resolves tagged and quoted !env strings, but never keys.
// Bound values remain strings and are not recursively interpreted as new tags.
func resolveSourceBindings(v any, bindings map[string]string, used map[string]bool, budget *int) (any, error) {
	switch x := v.(type) {
	case string:
		if strings.HasPrefix(x, "!env ") {
			name := strings.TrimSpace(strings.TrimPrefix(x, "!env "))
			value, ok := bindings[name]
			if !ok || value == "" {
				return nil, errSourceYAML
			}
			used[name] = true
			x = value
		}
		*budget += len(x)
		if *budget > 1<<20 {
			return nil, errSourceYAML
		}
		return x, nil
	case map[string]any:
		for k, v := range x {
			*budget += len(k)
			if *budget > 1<<20 {
				return nil, errSourceYAML
			}
			value, err := resolveSourceBindings(v, bindings, used, budget)
			if err != nil {
				return nil, err
			}
			x[k] = value
		}
		return x, nil
	case []any:
		for i, v := range x {
			value, err := resolveSourceBindings(v, bindings, used, budget)
			if err != nil {
				return nil, err
			}
			x[i] = value
		}
		return x, nil
	default:
		return v, nil
	}
}
