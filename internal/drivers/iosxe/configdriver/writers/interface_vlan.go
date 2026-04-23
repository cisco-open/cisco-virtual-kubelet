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

// SVI (Switched Virtual Interface) Phase-3 writer.
//
// netascode:
//   interface_vlan:
//     interfaces:
//       - name: 10
//         description: users SVI
//         ipv4_address: 10.0.10.1
//         ipv4_address_mask: 255.255.255.0
//         vrf: MGMT
//
// YANG: /Cisco-IOS-XE-native:native/interface/Vlan. Key: name
// (the VLAN ID).

func init() {
	Override(keyedListWriter{
		family:      "interface_vlan",
		yangPath:    "/Cisco-IOS-XE-native:native/interface/Vlan",
		envelopeKey: "Cisco-IOS-XE-native:Vlan",
		innerKey:    "interfaces",
		keyField:    "name",
		managedLeaves: []string{
			"description",
			"ipv4_address",
			"ipv4_address_mask",
			"vrf",
			"shutdown",
			"mtu",
			"ip",
		},
	})
}
