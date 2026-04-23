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

// Loopback interface Phase-1 writer.
//
// netascode shape:
//
//   interface_loopback:
//     interfaces:
//       - name: 0
//         description: router-id
//         ipv4_address: 10.255.255.1
//         ipv4_address_mask: 255.255.255.255
//         vrf: MGMT
//         shutdown: false
//
// YANG path: /Cisco-IOS-XE-native:native/interface/Loopback
// Key: name (string-rendered integer). Managed leaves: description,
// ipv4_address, ipv4_address_mask, vrf, shutdown.

func init() {
	Override(keyedListWriter{
		family:      "interface_loopback",
		yangPath:    "/Cisco-IOS-XE-native:native/interface/Loopback",
		envelopeKey: "Cisco-IOS-XE-native:Loopback",
		innerKey:    "interfaces",
		keyField:    "name",
		managedLeaves: []string{
			"description",
			"ipv4_address",
			"ipv4_address_mask",
			"vrf",
			"shutdown",
		},
	})
}
