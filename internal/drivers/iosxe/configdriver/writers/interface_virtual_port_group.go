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

// VirtualPortGroup Phase-1 writer. This is the interface type that
// carries IOx app-hosting traffic on IOS-XE, so it is one of the
// apphosting-prerequisite families CVK can auto-own.
//
// netascode shape:
//
//   interface_virtual_port_group:
//     interfaces:
//       - id: 0
//         description: iox-app-hosting
//         ipv4_address: 192.168.10.1
//         ipv4_address_mask: 255.255.255.0
//         shutdown: false
//
// YANG path: /Cisco-IOS-XE-native:native/interface/VirtualPortGroup
// Key: id. Managed leaves: description, ipv4_address,
// ipv4_address_mask, shutdown.

func init() {
	Override(keyedListWriter{
		family:      "interface_virtual_port_group",
		yangPath:    "/Cisco-IOS-XE-native:native/interface/VirtualPortGroup",
		envelopeKey: "Cisco-IOS-XE-native:VirtualPortGroup",
		innerKey:    "interfaces",
		keyField:    "id",
		managedLeaves: []string{
			"description",
			"ipv4_address",
			"ipv4_address_mask",
			"shutdown",
			"ip_helper_address",
		},
		yangBodyShape:  interfaceVPGToYANG,
		yangFetchShape: interfaceVPGFromYANG,
	})
}
