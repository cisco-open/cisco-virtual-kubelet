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

// Prefix-list writer with per-sequence diffing. Shares the nested-
// keyed-list machinery with the ACL writers; only the family-
// specific binding lives here.
//
// netascode:
//   prefix_list:
//     prefixes:
//       - name: DEFAULT-ONLY
//         description: default only
//         sequences:
//           - no: 10
//             action: permit
//             ip: 0.0.0.0/0
//
// YANG: /Cisco-IOS-XE-native:native/ip/prefix-list/prefixes. The
// inner list's YANG name is "seq" rather than "sequences"; we
// configure it explicitly so the device-decoded shape resolves.

func init() {
	Override(nestedKeyedListWriter{
		base: keyedListWriter{
			family:      "prefix_list",
			yangPath:    "/Cisco-IOS-XE-native:native/ip/prefix-list/prefixes",
			envelopeKey: "Cisco-IOS-XE-native:prefixes",
			innerKey:    "prefixes",
			keyField:    "name",
			managedLeaves: []string{
				"description",
				"sequences",
			},
		},
		nestedLeaf:      "sequences",
		nestedKeyField:  "no",
		nestedYANGInner: "seq",
	})
}
