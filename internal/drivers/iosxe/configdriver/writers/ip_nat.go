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

// NAT Phase-3 writer set.
//
// ip_nat_inside_source manages rules under /ip/nat/inside/source
// (typically 'list acl pool poolname overload'-style rules).
// ip_nat_pool manages the named address pools referenced by those
// rules.

func init() {
	Override(singletonWriter{
		family:      "ip_nat_inside_source",
		yangPath:    "/Cisco-IOS-XE-native:native/ip/nat/inside/source",
		envelopeKey: "Cisco-IOS-XE-nat:source",
		managedLeaves: []string{
			"list",
			"static",
			"route-map",
		},
	})

	Override(keyedListWriter{
		family:      "ip_nat_pool",
		yangPath:    "/Cisco-IOS-XE-native:native/ip/nat/pool",
		envelopeKey: "Cisco-IOS-XE-nat:pool",
		innerKey:    "pools",
		keyField:    "id",
		managedLeaves: []string{
			"start-address",
			"end-address",
			"netmask",
			"prefix-length",
			"type",
		},
	})
}
