// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package intent resolves IOSXEConfig CRs into a ResolvedIntent the config
// driver can act on. It merges scopes (defaults → device-group → template →
// per-device) per netascode semantics, loads source payloads (inline vs
// ConfigMap), expands IOSXETemplate fragments, and produces a canonical
// hash used by the reconciler's short-circuit.
package intent

import (
	"fmt"
	"reflect"
)

// KeyRules overrides the fallback key-field detection with
// path-specific knowledge drawn from the family index. Keys are
// dot-separated paths rooted at the merge input (e.g. "vlan.vlans",
// "interface_ethernet.interfaces"); values are the list-element field
// the merger must treat as identity. A non-empty rule wins over the
// fallback heuristic below.
type KeyRules map[string]string

// Merge deep-merges src into dst per netascode precedence rules. Without
// explicit rules, the default heuristic applies. See MergeWithRules for
// family-aware merging.
func Merge(dst, src any) any {
	return mergeAt(nil, nil, dst, src)
}

// MergeWithRules deep-merges src into dst using the supplied KeyRules
// to identify list elements for known paths. Paths in the rule map are
// matched as exact prefixes against the traversal path; the outermost
// match wins. Falls back to the heuristic when no rule applies.
//
// Semantics:
//
//   - Objects (maps): recurse key-by-key. Keys present only in dst
//     survive; keys in both are recursed; keys only in src are adopted.
//   - Keyed lists: src entries replace or extend dst entries with the
//     same key; unknown-key entries are appended in src order.
//   - Scalar / mixed lists: src replaces dst outright.
//   - Scalars: src overrides dst.
//   - Nil: a nil src leaves dst untouched; a nil dst takes src verbatim.
//
// The returned value is deep-copied — neither input is mutated, so
// callers can cache scope inputs across reconciles without aliasing.
func MergeWithRules(dst, src any, rules KeyRules) any {
	return mergeAt(nil, rules, dst, src)
}

func mergeAt(path []string, rules KeyRules, dst, src any) any {
	if src == nil {
		return deepCopy(dst)
	}
	if dst == nil {
		return deepCopy(src)
	}

	switch dv := dst.(type) {
	case map[string]any:
		sv, ok := src.(map[string]any)
		if !ok {
			return deepCopy(src)
		}
		return mergeMaps(path, rules, dv, sv)
	case []any:
		sv, ok := src.([]any)
		if !ok {
			return deepCopy(src)
		}
		return mergeLists(path, rules, dv, sv)
	default:
		return deepCopy(src)
	}
}

func mergeMaps(path []string, rules KeyRules, dst, src map[string]any) map[string]any {
	out := make(map[string]any, len(dst)+len(src))
	for k, v := range dst {
		out[k] = deepCopy(v)
	}
	for k, v := range src {
		childPath := append(append([]string(nil), path...), k)
		if existing, ok := out[k]; ok {
			out[k] = mergeAt(childPath, rules, existing, v)
		} else {
			out[k] = deepCopy(v)
		}
	}
	return out
}

func mergeLists(path []string, rules KeyRules, dst, src []any) []any {
	if keyField, explicit := keyFromRules(path, rules); explicit {
		if keyField == "" {
			// Rule says "treat as opaque" — rightmost wins.
			return deepCopy(src).([]any)
		}
		if listElementsHaveKey(dst, keyField) && listElementsHaveKey(src, keyField) {
			return mergeKeyedLists(path, rules, keyField, dst, src)
		}
		return deepCopy(src).([]any)
	}
	keyField, keyed := detectKeyField(dst, src)
	if !keyed {
		return deepCopy(src).([]any)
	}
	return mergeKeyedLists(path, rules, keyField, dst, src)
}

// keyFromRules looks up the current path in rules. It returns the
// configured key and true if a rule matches. The rule map's keys are
// dot-separated paths matched exactly against path.
func keyFromRules(path []string, rules KeyRules) (string, bool) {
	if len(rules) == 0 || len(path) == 0 {
		return "", false
	}
	joined := joinPath(path)
	if k, ok := rules[joined]; ok {
		return k, true
	}
	return "", false
}

func joinPath(path []string) string {
	out := ""
	for i, p := range path {
		if i > 0 {
			out += "."
		}
		out += p
	}
	return out
}

// keyFieldCandidates is the fallback ordered list used when no rule
// matches the current path. Chosen to align with netascode's own merge
// tool: name > id > sequence > type.
var keyFieldCandidates = []string{"name", "id", "sequence", "type"}

func detectKeyField(dst, src []any) (string, bool) {
	if len(dst) == 0 || len(src) == 0 {
		return "", false
	}
	for _, candidate := range keyFieldCandidates {
		if listElementsHaveKey(dst, candidate) && listElementsHaveKey(src, candidate) {
			return candidate, true
		}
	}
	return "", false
}

func listElementsHaveKey(list []any, key string) bool {
	for _, el := range list {
		m, ok := el.(map[string]any)
		if !ok {
			return false
		}
		if _, present := m[key]; !present {
			return false
		}
	}
	return true
}

func mergeKeyedLists(path []string, rules KeyRules, keyField string, dst, src []any) []any {
	// Preserve order: dst entries first in original order; src entries with
	// the same key replace (deep-merged) the corresponding dst entry;
	// src entries with new keys are appended in src order.
	index := map[string]int{}
	out := make([]any, 0, len(dst)+len(src))
	for _, el := range dst {
		m, _ := el.(map[string]any)
		k := fmt.Sprintf("%v", m[keyField])
		index[k] = len(out)
		out = append(out, deepCopy(m))
	}
	for _, el := range src {
		m, _ := el.(map[string]any)
		k := fmt.Sprintf("%v", m[keyField])
		if pos, ok := index[k]; ok {
			merged := mergeAt(path, rules, out[pos], m)
			out[pos] = merged
		} else {
			index[k] = len(out)
			out = append(out, deepCopy(m))
		}
	}
	return out
}

// deepCopy produces a structurally-independent copy of a netascode value
// tree. It handles the subset of types that survive YAML → map[string]any
// decoding (maps, slices, strings, numbers, booleans, nil). Unknown types
// are returned as-is — callers should not pass concrete Go values into
// Merge; the contract is decoded YAML.
func deepCopy(v any) any {
	if v == nil {
		return nil
	}
	switch tv := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(tv))
		for k, val := range tv {
			out[k] = deepCopy(val)
		}
		return out
	case []any:
		out := make([]any, len(tv))
		for i, val := range tv {
			out[i] = deepCopy(val)
		}
		return out
	default:
		return tv
	}
}

// Equal reports whether two netascode-shaped values are structurally
// equivalent. Used by the reconciler to detect "no-op" plans without
// re-serialising and hashing. reflect.DeepEqual is sufficient because
// deepCopy normalises the type set.
func Equal(a, b any) bool { return reflect.DeepEqual(a, b) }
