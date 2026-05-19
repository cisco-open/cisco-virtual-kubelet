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
// YANG: /Cisco-IOS-XE-native:native/ip/pim (augmented by Cisco-IOS-XE-multicast)
//
// NOTE: Requires ip multicast-routing to be enabled on the device.

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

// pimBodyShape adds the Cisco-IOS-XE-multicast: module prefix to
// each key in the flat map, since these leaves come from an augment
// of the Cisco-IOS-XE-multicast module into /native/ip/pim.
func pimBodyShape(flat map[string]any) map[string]any {
	const prefix = "Cisco-IOS-XE-multicast:"
	out := make(map[string]any, len(flat))
	for k, v := range flat {
		out[prefix+k] = v
	}
	return out
}

// pimFetchShape strips the Cisco-IOS-XE-multicast: module prefix
// from fetched keys so comparison uses the flat user-facing names.
func pimFetchShape(fetched map[string]any) map[string]any {
	const prefix = "Cisco-IOS-XE-multicast:"
	out := make(map[string]any, len(fetched))
	for k, v := range fetched {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			out[k[len(prefix):]] = v
		} else {
			out[k] = v
		}
	}
	return out
}

func init() {
	Override(singletonWriter{
		family:         "pim",
		yangPath:       "/Cisco-IOS-XE-native:native/ip/pim",
		envelopeKey:    "Cisco-IOS-XE-native:pim",
		managedLeaves:  pimLeaves,
		yangBodyShape:  pimBodyShape,
		yangFetchShape: pimFetchShape,
	})
}

func pimManagedLeaves() []string { return append([]string(nil), pimLeaves...) }
