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

import (
	"fmt"
	"strings"
)

// IPv6 PIM writer.
//
// netascode shape:
//
//	ipv6_pim:
//	  rp-address: "2001:DB8::100"
//
// YANG: /Cisco-IOS-XE-native:native/ipv6/pim (augmented by Cisco-IOS-XE-multicast)

var ipv6PIMLeaves = []string{
	"rp-address",
}

func ipv6PIMBodyShape(flat map[string]any) map[string]any {
	const prefix = "Cisco-IOS-XE-multicast:"
	out := make(map[string]any)

	if v, ok := flat["rp-address"]; ok {
		out[prefix+"rp-address"] = map[string]any{"address": fmt.Sprintf("%v", v)}
	}

	return out
}

func ipv6PIMFetchShape(fetched map[string]any) map[string]any {
	const prefix = "Cisco-IOS-XE-multicast:"
	out := make(map[string]any)

	for k, v := range fetched {
		key := k
		if strings.HasPrefix(k, prefix) {
			key = k[len(prefix):]
		}
		switch key {
		case "rp-address":
			if m, ok := v.(map[string]any); ok {
				if addr, ok := m["address"]; ok {
					out["rp-address"] = addr
				}
			}
		default:
			out[key] = v
		}
	}
	return out
}

func init() {
	Override(singletonWriter{
		family:         "ipv6_pim",
		yangPath:       "/Cisco-IOS-XE-native:native/ipv6/pim",
		envelopeKey:    "Cisco-IOS-XE-native:pim",
		managedLeaves:  ipv6PIMLeaves,
		yangBodyShape:  ipv6PIMBodyShape,
		yangFetchShape: ipv6PIMFetchShape,
	})
}

func ipv6PimManagedLeaves() []string {
	return append([]string(nil), ipv6PIMLeaves...)
}
