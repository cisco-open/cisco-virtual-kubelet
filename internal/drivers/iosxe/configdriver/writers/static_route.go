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

// Static IPv4 route Phase-2 writer.
//
// netascode:
//   static_route:
//     routes:
//       - prefix: 0.0.0.0
//         mask: 0.0.0.0
//         next_hop: 192.0.2.1
//         distance: 1
//
// YANG: /Cisco-IOS-XE-native:native/ip/route/ip-route-interface-forwarding-list
// (the specific container depends on whether the route is
// prefix-based or interface-bound; Phase-2 handles the common
// prefix+next_hop case). Key is (prefix, mask) — we collapse to the
// prefix leaf for RESTCONF key-composition since that matches what
// netascode emits in practice.
//
// Phase-3 will model the longer key form and interface-bound routes.

func init() {
	Override(keyedListWriter{
		family:      "static_route",
		yangPath:    "/Cisco-IOS-XE-native:native/ip/route/ip-route-interface-forwarding-list",
		envelopeKey: "Cisco-IOS-XE-native:ip-route-interface-forwarding-list",
		innerKey:    "routes",
		keyField:    "prefix",
		managedLeaves: []string{
			"mask",
			"fwd-list",
			"distance",
			"tag",
			"description",
		},
	})
}
