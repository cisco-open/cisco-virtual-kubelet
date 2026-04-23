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

// OSPF Phase-2 writer.
//
// netascode (Phase-2 subset):
//
//   ospf:
//     processes:
//       - id: 1
//         router_id: 10.255.255.1
//         networks:
//           - prefix: 10.0.0.0
//             wildcard: 0.0.0.255
//             area: 0
//         redistribute:
//           connected:
//             enabled: true
//
// YANG: /Cisco-IOS-XE-native:native/router/Cisco-IOS-XE-ospf:router-ospf/ospf
// Key: id (process number).
//
// Each OSPF process is a deep subtree; Phase-2 treats the per-process
// network/redistribute/area lists as opaque managed leaves, so
// changes to those lists still trigger a merge, but per-entry
// diffing is a Phase-3 deliverable.

func init() {
	Override(keyedListWriter{
		family:      "ospf",
		yangPath:    "/Cisco-IOS-XE-native:native/router/Cisco-IOS-XE-ospf:router-ospf/ospf/process-id",
		envelopeKey: "Cisco-IOS-XE-ospf:process-id",
		innerKey:    "processes",
		keyField:    "id",
		managedLeaves: []string{
			"router-id",
			"network",
			"redistribute",
			"area",
			"auto-cost",
			"passive-interface",
		},
	})
}
