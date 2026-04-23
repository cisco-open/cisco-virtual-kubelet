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

// Route-map Phase-2 writer.
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
// YANG: /Cisco-IOS-XE-native:native/route-map. The entries list is
// an opaque managed leaf in Phase-2; per-sequence diffing is
// follow-up.

func init() {
	Override(keyedListWriter{
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
	})
}
