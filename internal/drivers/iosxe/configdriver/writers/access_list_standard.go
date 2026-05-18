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

package writers

// Standard access-list writer with per-rule sequence-keyed diffing.
// Shares the nested-keyed-list machinery with access_list_extended.
//
// netascode:
//   access_list_standard:
//     standard:
//       - name: MGMT-IN
//         rules:
//           - sequence: 10
//             action: permit
//             source_any: true

func init() {
	Override(nestedKeyedListWriter{
		base: keyedListWriter{
			family: "access_list_standard",
			// `standard` lives in the Cisco-IOS-XE-acl YANG module —
			// path's last segment must carry the qualified prefix
			// so the netconf transport's path-aware filter builder
			// xmlns-declares the element. Mirrors the
			// access_list_extended fix landed in commit a816311.
			yangPath:      "/Cisco-IOS-XE-native:native/ip/access-list/Cisco-IOS-XE-acl:standard",
			envelopeKey:   "Cisco-IOS-XE-acl:standard",
			innerKey:      "standard",
			keyField:      "name",
			managedLeaves: []string{"rules"},
		},
		nestedLeaf:       "rules",
		nestedKeyField:   "sequence",
		nestedYANGInner:  "access-list-seq-rule",
		nestedBodyShape:  stdAclRuleToYANG,
		nestedFetchShape: stdAclRuleFromYANG,
	})
}

// stdAclRuleToYANG converts a netascode standard-ACL rule to the
// IOS-XE-acl YANG body. The standard ACL schema on all tested
// IOS-XE versions (17.16+) uses:
//
//	{sequence, permit|deny: {std-ace: {source-fields}}}
//
// Source field mapping:
//
//	src_any / source_any  → any: [null]
//	src_host / source_host → host: <addr>
//	src_prefix + src_wildcard → ipv4-address-prefix + mask
func stdAclRuleToYANG(flat map[string]any) map[string]any {
	emptyLeaf := []any{nil}
	out := map[string]any{}
	ace := map[string]any{}
	var action string

	for k, v := range flat {
		switch k {
		case "sequence":
			out["sequence"] = v
		case "action":
			if s, ok := v.(string); ok {
				action = s
			}
		case "src_any", "source_any":
			if isTrue(v) {
				ace["any"] = emptyLeaf
			}
		case "src_host", "source_host":
			ace["host"] = v
		case "src_prefix", "source_prefix":
			ace["ipv4-address-prefix"] = v
		case "src_wildcard", "source_wildcard":
			ace["mask"] = v
		case "log":
			if isTrue(v) {
				ace["log"] = emptyLeaf
			}
		default:
			// pass through unmapped keys
			ace[k] = v
		}
	}

	if action != "" && len(ace) > 0 {
		out[action] = map[string]any{"std-ace": ace}
	}
	return out
}

// stdAclRuleFromYANG inverts stdAclRuleToYANG for the Fetch path.
// Converts the device's {sequence, permit|deny: {std-ace: {...}}}
// back to the flat netascode shape {sequence, action, src_*}.
func stdAclRuleFromYANG(yang map[string]any) map[string]any {
	out := map[string]any{}

	if seq, ok := yang["sequence"]; ok {
		out["sequence"] = seq
	}

	// Extract action + std-ace container.
	var action string
	var ace map[string]any
	for _, a := range []string{"permit", "deny"} {
		if container, ok := yang[a]; ok {
			action = a
			if m, ok := container.(map[string]any); ok {
				if inner, ok := m["std-ace"].(map[string]any); ok {
					ace = inner
				}
			}
			break
		}
	}

	if action != "" {
		out["action"] = action
	}
	if ace == nil {
		return out
	}

	// Reverse-map source fields.
	for k, v := range ace {
		switch k {
		case "any":
			if isTrue(v) {
				out["src_any"] = true
			}
		case "host":
			out["src_host"] = v
		case "ipv4-address-prefix":
			out["src_prefix"] = v
		case "mask":
			out["src_wildcard"] = v
		case "log":
			if isTrue(v) {
				out["log"] = true
			}
		default:
			out[k] = v
		}
	}
	return out
}
