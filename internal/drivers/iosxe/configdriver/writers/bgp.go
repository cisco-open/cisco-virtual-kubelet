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

// BGP Phase-2 writer.
//
// netascode shape (narrow, Phase-2 subset):
//
//   bgp:
//     asn: "65000"
//     router_id: 10.255.255.1
//     log_neighbor_changes: true
//     neighbors:
//       - id: 192.0.2.2
//         remote_as: "65001"
//         description: peer-1
//         address_families:
//           ipv4_unicast:
//             activate: true
//
// YANG: /Cisco-IOS-XE-native:native/router/Cisco-IOS-XE-bgp:router-bgp
//
// BGP is the single most complex family in Cisco-IOS-XE YANG —
// attempting to model every leaf would produce thousands of lines of
// writer code and very few Phase-2 users would exercise them. The
// Phase-2 writer treats the container as a singleton with a managed-
// leaf set covering the knobs most commonly set via netascode. Deeper
// neighbor/address-family shaping is the work of Phase-3, when real
// operator repos will inform the priority order.
//
// The Key label 'singleton' in families.yaml reflects this: the YANG
// subtree itself is keyed by an AS number, but netascode expresses
// only a single BGP instance per device so the writer treats the
// entire router-bgp container as the managed scope.

func init() {
	Override(singletonWriter{
		family:      "bgp",
		yangPath:    "/Cisco-IOS-XE-native:native/router/Cisco-IOS-XE-bgp:router-bgp",
		envelopeKey: "Cisco-IOS-XE-bgp:router-bgp",
		managedLeaves: []string{
			"id",
			"bgp",
			"neighbor",
			"address-family",
			"redistribute",
		},
	})
}
