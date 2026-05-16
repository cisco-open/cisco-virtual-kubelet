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

// VRF Phase-1 writer.
//
// netascode shape:
//
//   vrf:
//     vrfs:
//       - name: MGMT
//         rd: "65000:1"
//         description: out-of-band
//         address_family_ipv4: true   # required before any
//                                     # interface can bind via
//                                     # `vrf forwarding MGMT`
//         address_family_ipv6: false
//
// YANG path: /Cisco-IOS-XE-native:native/vrf/definition
// Key: name. Managed leaves: rd, description,
// address_family_ipv4, address_family_ipv6.
//
// Address-family modeling matters because IOS-XE rejects a
// `vrf forwarding <name>` binding on an interface unless the
// VRF has the matching address-family enabled
// ("Address-family ipv4 in VRF must be created 1st, deleted last").
// Live retest of test 09 + 13 surfaced this as a hard constraint.

func init() {
	Override(keyedListWriter{
		family:      "vrf",
		yangPath:    "/Cisco-IOS-XE-native:native/vrf/definition",
		envelopeKey: "Cisco-IOS-XE-native:definition",
		innerKey:    "vrfs",
		keyField:    "name",
		managedLeaves: []string{
			"rd", "description",
			"address_family_ipv4", "address_family_ipv6",
		},
		yangBodyShape:  vrfToYANG,
		yangFetchShape: vrfFromYANG,
	})
}

// vrfToYANG translates netascode flat leaves to the
// Cisco-IOS-XE-native VRF body. The two address_family_* booleans
// become presence containers under the YANG `address-family`
// parent.
//
// Wire shape produced for `address_family_ipv4: true`:
//
//	address-family:
//	  ipv4: {}
func vrfToYANG(flat map[string]any) map[string]any {
	out := make(map[string]any, len(flat))
	for k, v := range flat {
		switch k {
		case "address_family_ipv4":
			if isTrue(v) {
				ensureAddressFamily(out)["ipv4"] = map[string]any{}
			}
		case "address_family_ipv6":
			if isTrue(v) {
				ensureAddressFamily(out)["ipv6"] = map[string]any{}
			}
		default:
			out[k] = v
		}
	}
	return out
}

// vrfFromYANG inverts vrfToYANG so observed device state can be
// compared to desired intent via leavesEqual on the flat shape.
func vrfFromYANG(yang map[string]any) map[string]any {
	out := make(map[string]any, len(yang))
	for k, v := range yang {
		if k != "address-family" {
			out[k] = v
			continue
		}
		af, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if _, has := af["ipv4"]; has {
			out["address_family_ipv4"] = true
		}
		if _, has := af["ipv6"]; has {
			out["address_family_ipv6"] = true
		}
	}
	return out
}

func ensureAddressFamily(out map[string]any) map[string]any {
	af, ok := out["address-family"].(map[string]any)
	if !ok {
		af = map[string]any{}
		out["address-family"] = af
	}
	return af
}

// isTrue accepts the YAML truth-shapes the netascode resolver
// surfaces — bool true, the string "true" / "yes", non-zero ints.
func isTrue(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "yes" || t == "1"
	case int:
		return t != 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	case []any:
		// YANG empty-leaf encoding: [null] is truthy.
		return len(t) == 1 && t[0] == nil
	default:
		return false
	}
}
