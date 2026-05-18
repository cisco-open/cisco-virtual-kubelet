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

// IPv4 PIM global Phase-3 writer.
//
// netascode shape (YANG keys used directly):
//
//	pim:
//	  autorp: false
//	  autorp-listener: false
//	  bsr-candidate:
//	    interface: Loopback150
//	    mask-length: 24
//	    priority: 10
//	  rp-address: 192.168.1.1
//	  rp-candidate:
//	    - interface: Loopback151
//	      interval: 100
//	      priority: 10
//	      bidir: {}
//	  ssm:
//	    range: ACL1
//	  register-source: Loopback152
//
// YANG: /Cisco-IOS-XE-native:native/ip/Cisco-IOS-XE-multicast:pim
//
// NOTE: Requires ip multicast-routing to be enabled on the device.
// On virtual C8000V/C9KV lab instances the YANG path may not be present
// (returns "uri keypath not found"). Tested against physical Cat9k/ISR
// hardware with IP Services / IP Multicast licence.

var pimLeaves = []string{
	"autorp",
	"autorp-listener",
	"bsr-candidate",
	"rp-address",
	"rp-addresses",
	"rp-candidate",
	"ssm",
	"register-source",
	"vrfs",
}

func init() {
	Override(singletonWriter{
		family:        "pim",
		yangPath:      "/Cisco-IOS-XE-native:native/ip/Cisco-IOS-XE-multicast:pim",
		envelopeKey:   "Cisco-IOS-XE-multicast:pim",
		managedLeaves: pimLeaves,
	})
}

func pimManagedLeaves() []string { return append([]string(nil), pimLeaves...) }
