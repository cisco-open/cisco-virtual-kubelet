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

// Port-channel interface Phase-3 writer.

func init() {
	Override(keyedListWriter{
		family:      "interface_port_channel",
		yangPath:    "/Cisco-IOS-XE-native:native/interface/Port-channel",
		envelopeKey: "Cisco-IOS-XE-native:Port-channel",
		innerKey:    "interfaces",
		keyField:    "name",
		managedLeaves: []string{
			"description",
			"ipv4_address",
			"ipv4_address_mask",
			"vrf",
			"shutdown",
			"mtu",
			"switchport",
		},
		yangBodyShape:  interfaceIPv4VRFToYANG,
		yangFetchShape: interfaceIPv4VRFFromYANG,
	})
}
