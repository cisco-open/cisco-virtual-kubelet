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

// Route-map writer with per-entry sequence diffing.
//
// netascode:
//   route_map:
//     route_maps:
//       - name: FROM-CUSTOMER-IN
//         entries:
//           - seq: 10
//             operation: permit
//             match:
//               ip_address_prefix_list: [DEFAULT-ONLY]
//             set:
//               local_preference: 200
//
// YANG: /Cisco-IOS-XE-native:native/route-map. Per-sequence diffing
// matters here too — a single match/set tweak on entry 10 must not
// rewrite a 200-entry route-map.

func init() {
	Override(nestedKeyedListWriter{
		base: keyedListWriter{
			family:      "route_map",
			yangPath:    "/Cisco-IOS-XE-native:native/route-map",
			envelopeKey: "Cisco-IOS-XE-native:route-map",
			innerKey:    "route_maps",
			keyField:    "name",
			managedLeaves: []string{
				"description",
				"entries",
				"route-map-without-order-seq",
			},
		},
		nestedLeaf:      "entries",
		nestedKeyField:  "seq",
		nestedYANGInner: "route-map-without-order-seq",
	})
}
