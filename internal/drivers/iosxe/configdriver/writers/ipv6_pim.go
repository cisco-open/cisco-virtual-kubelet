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

// IPv6 PIM global Phase-3 writer.
//
// netascode shape (YANG keys used directly):
//
//	ipv6_pim:
//	  rp-address:
//	    - address: 2001:db8::100
//	      access-list: ff70::/12
//	      bidir: {}
//	  vrfs:
//	    - name: IPV6_PIM_VRF
//	      rp-address:
//	        - address: 2001:db8:154::1
//	          access-list: ff71::/12
//
// YANG: /Cisco-IOS-XE-native:native/ipv6/Cisco-IOS-XE-multicast:pim
//
// NOTE: Requires ipv6 multicast-routing to be enabled on the device.
// On virtual C8000V/C9KV lab instances the YANG path may not be present.
// Tested against physical hardware with IP Services licence.

var ipv6PimLeaves = []string{
	"rp-address",
	"vrfs",
}

func init() {
	Override(singletonWriter{
		family:        "ipv6_pim",
		yangPath:      "/Cisco-IOS-XE-native:native/ipv6/Cisco-IOS-XE-multicast:pim",
		envelopeKey:   "Cisco-IOS-XE-multicast:pim",
		managedLeaves: ipv6PimLeaves,
	})
}

func ipv6PimManagedLeaves() []string { return append([]string(nil), ipv6PimLeaves...) }
