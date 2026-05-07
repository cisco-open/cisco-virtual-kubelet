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

// Standard IPv6 access-list Phase-3 writer.
//
// netascode:
//   ipv6_access_list_standard:
//     acls:
//       - name: MGMT6
//         rules:
//           - sequence: 10
//             action: permit
//             source: any
//
// YANG: /Cisco-IOS-XE-native:native/ipv6/access-list. Note that the
// IPv6 ACL namespace overlaps with ipv6_access_list_extended; the
// device distinguishes standard vs extended by the rule shape.

func init() {
	Override(keyedListWriter{
		family:        "ipv6_access_list_standard",
		yangPath:      "/Cisco-IOS-XE-native:native/ipv6/access-list",
		envelopeKey:   "Cisco-IOS-XE-acl:access-list",
		innerKey:      "acls",
		keyField:      "name",
		managedLeaves: []string{"rules"},
	})
}
