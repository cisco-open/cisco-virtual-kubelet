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
		nestedLeaf:      "rules",
		nestedKeyField:  "sequence",
		nestedYANGInner: "access-list-seq-rule",
		nestedBodyShape: aclRuleToYANG,
	})
}
