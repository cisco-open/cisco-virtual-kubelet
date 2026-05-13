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

package intent

// ──────────────────────────────────────────────────────────────────
// YAML 1.1 boolean key fixup
//
// sigs.k8s.io/yaml (and by extension gopkg.in/yaml.v2) implements
// YAML 1.1, which treats bare tokens yes/no/on/off/true/false as
// booleans. When such a token appears as a *map key*, the YAML→JSON
// conversion converts it to the JSON boolean string:
//
//     no: 10   →   { "false": 10 }
//     yes: 10  →   { "true": 10 }
//
// In the IOS-XE netascode schema the YANG leaf name "no" is used as
// a sequence number key in prefix-list entries. After Kubernetes API
// server round-trips (kubectl apply → etcd → runtime.RawExtension)
// the damage is already done: the key arrives as "false".
//
// This file provides a recursive tree walk that renames the mangled
// keys back to their YANG-canonical names. The walk is applied once
// at the end of Resolve(), after all YAML sources have been merged.
// ──────────────────────────────────────────────────────────────────

// yaml11BoolKeyMap maps YAML 1.1 mangled boolean key strings back to
// their original YANG leaf names. Only keys that actually appear in
// the IOS-XE netascode schema need entries here.
var yaml11BoolKeyMap = map[string]string{
	"false": "no",
}

// FixYAML11BoolKeys recursively walks a decoded YAML tree and renames
// map keys that were mangled by YAML 1.1 boolean interpretation.
// The walk modifies maps in place and returns the (potentially
// modified) root value.
func FixYAML11BoolKeys(v any) any {
	switch tv := v.(type) {
	case map[string]any:
		fixYAML11Map(tv)
		return tv
	case []any:
		for i, el := range tv {
			tv[i] = FixYAML11BoolKeys(el)
		}
		return tv
	default:
		return v
	}
}

// fixYAML11Map renames mangled keys in a single map and recurses
// into all values.
func fixYAML11Map(m map[string]any) {
	// Phase 1: collect renames to avoid mutating during iteration.
	for mangled, canonical := range yaml11BoolKeyMap {
		if _, hasCanonical := m[canonical]; hasCanonical {
			continue // already has the correct key — don't overwrite
		}
		if v, ok := m[mangled]; ok {
			m[canonical] = v
			delete(m, mangled)
		}
	}
	// Phase 2: recurse into values.
	for _, v := range m {
		FixYAML11BoolKeys(v)
	}
}
