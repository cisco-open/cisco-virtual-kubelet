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

// Extended access-list Phase-1 writer.
//
// netascode shape:
//
//   access_list_extended:
//     extended:
//       - name: IOX-INGRESS
//         rules:
//           - sequence: 10
//             action: permit
//             protocol: ip
//             src_any: true
//             dst_any: true
//
// YANG path: /Cisco-IOS-XE-native:native/ip/access-list/extended
// Key: name. The rule list nested inside each entry is managed as
// an opaque blob by Phase-1 (equality-compare the entire rules list).
// Phase-2 introduces per-rule diffing keyed by sequence.

func init() {
	Override(keyedListWriter{
		family:      "access_list_extended",
		yangPath:    "/Cisco-IOS-XE-native:native/ip/access-list/extended",
		envelopeKey: "Cisco-IOS-XE-acl:extended",
		innerKey:    "extended",
		keyField:    "name",
		// "rules" is treated as a managed leaf — if the slice differs, a
		// merge op replaces the ACL body. The engine's additive-only
		// semantics still apply: ACLs on the device that are not in the
		// intent are not touched.
		managedLeaves: []string{"rules"},
	})
}
