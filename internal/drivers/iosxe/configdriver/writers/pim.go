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
	"strconv"
	"strings"
)

// IPv4 PIM global Phase-3 writer.
//
// netascode shape (flat user-facing keys):
//
//	pim:
//	  autorp: false
//	  autorp-listener: false
//	  bsr-candidate:
//	    interface: Loopback150
//	    mask-length: 24
//	    priority: 10
//	  rp-address: 192.168.1.1
//	  rp-addresses:
//	    - access-list: "10"
//	      rp-address: "10.10.10.10"
//	  rp-candidate:
//	    - interface: Loopback151
//	      interval: 100
//	      priority: 10
//	  ssm:
//	    range: ACL1
//	  register-source: Loopback152
//	  vrfs:
//	    - vrf: VRF1
//	      autorp: true
//	      ...
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

// pimBodyShape transforms netascode flat format → YANG wire format.
func pimBodyShape(flat map[string]any) map[string]any {
	const prefix = "Cisco-IOS-XE-multicast:"
	out := make(map[string]any)

	// autorp + autorp-listener → autorp-container
	autorpContainer := map[string]any{}
	if v, ok := flat["autorp"]; ok {
		autorpContainer["autorp"] = v
	}
	if v, ok := flat["autorp-listener"]; ok {
		if b, ok := v.(bool); ok && b {
			autorpContainer["listener"] = []any{nil}
		}
	}
	if len(autorpContainer) > 0 {
		out[prefix+"autorp-container"] = autorpContainer
	}

	// bsr-candidate: {interface: "Loopback150", mask-length: 24, priority: 10}
	// → bsr-candidate: {Loopback: 150, mask: 24, priority: 10}
	if bsr, ok := flat["bsr-candidate"].(map[string]any); ok {
		out[prefix+"bsr-candidate"] = interfaceToYANG(bsr, "mask-length", "mask")
	}

	// rp-address: "192.168.1.1" → rp-address-conf: {address: "192.168.1.1"}
	if v, ok := flat["rp-address"]; ok {
		addr := fmt.Sprintf("%v", v)
		out[prefix+"rp-address-conf"] = map[string]any{"address": addr}
	}

	// rp-addresses: [...] → rp-address-list: [...]
	if v, ok := flat["rp-addresses"]; ok {
		out[prefix+"rp-address-list"] = v
	}

	// rp-candidate: [...] → rp-candidate: [...] (same shape)
	if v, ok := flat["rp-candidate"]; ok {
		out[prefix+"rp-candidate"] = v
	}

	// ssm: {range: "ACL"} → ssm: {range: "ACL"} (same)
	if v, ok := flat["ssm"]; ok {
		out[prefix+"ssm"] = v
	}

	// register-source: "Loopback152" → register-source: {Loopback: 152}
	if v, ok := flat["register-source"]; ok {
		out[prefix+"register-source"] = interfaceStringToYANG(fmt.Sprintf("%v", v))
	}

	// vrfs: [{vrf: "VRF1", ...}] → vrf: [{id: "VRF1", ...}]
	if vrfs, ok := flat["vrfs"]; ok {
		if vrfList, ok := vrfs.([]any); ok {
			yangVRFs := make([]any, 0, len(vrfList))
			for _, item := range vrfList {
				if m, ok := item.(map[string]any); ok {
					yangVRFs = append(yangVRFs, vrfEntryToYANG(m))
				}
			}
			out[prefix+"vrf"] = yangVRFs
		}
	}

	return out
}

// pimFetchShape transforms YANG wire format → netascode flat format.
func pimFetchShape(fetched map[string]any) map[string]any {
	const prefix = "Cisco-IOS-XE-multicast:"
	out := make(map[string]any)

	for k, v := range fetched {
		key := k
		if strings.HasPrefix(k, prefix) {
			key = k[len(prefix):]
		}
		switch key {
		case "autorp-container":
			if m, ok := v.(map[string]any); ok {
				if a, ok := m["autorp"]; ok {
					out["autorp"] = a
				}
				if _, ok := m["listener"]; ok {
					out["autorp-listener"] = true
				}
			}
		case "bsr-candidate":
			if m, ok := v.(map[string]any); ok {
				out["bsr-candidate"] = interfaceFromYANG(m, "mask", "mask-length")
			}
		case "rp-address-conf":
			if m, ok := v.(map[string]any); ok {
				if addr, ok := m["address"]; ok {
					out["rp-address"] = addr
				}
			}
		case "rp-address-list":
			out["rp-addresses"] = v
		case "rp-candidate":
			out["rp-candidate"] = v
		case "ssm":
			out["ssm"] = v
		case "register-source":
			if m, ok := v.(map[string]any); ok {
				out["register-source"] = interfaceYANGToString(m)
			}
		case "vrf":
			if vrfList, ok := v.([]any); ok {
				vrfs := make([]any, 0, len(vrfList))
				for _, item := range vrfList {
					if m, ok := item.(map[string]any); ok {
						vrfs = append(vrfs, vrfEntryFromYANG(m))
					}
				}
				out["vrfs"] = vrfs
			}
		default:
			out[key] = v
		}
	}
	return out
}

// interfaceToYANG converts {interface: "Loopback150", mask-length: 24, ...}
// → {Loopback: 150, mask: 24, ...}
func interfaceToYANG(m map[string]any, fromKey, toKey string) map[string]any {
	out := make(map[string]any, len(m))
	if iface, ok := m["interface"]; ok {
		ifStr := fmt.Sprintf("%v", iface)
		ifMap := interfaceStringToYANG(ifStr)
		for k, v := range ifMap {
			out[k] = v
		}
	}
	for k, v := range m {
		if k == "interface" {
			continue
		}
		if k == fromKey {
			out[toKey] = v
		} else {
			out[k] = v
		}
	}
	return out
}

// interfaceFromYANG reverses interfaceToYANG.
func interfaceFromYANG(m map[string]any, fromKey, toKey string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch {
		case isInterfaceType(k):
			out["interface"] = interfaceYANGToString(map[string]any{k: v})
		case k == fromKey:
			out[toKey] = v
		default:
			out[k] = v
		}
	}
	return out
}

// interfaceStringToYANG converts "Loopback150" → {"Loopback": 150}
func interfaceStringToYANG(s string) map[string]any {
	for _, prefix := range []string{"Loopback", "GigabitEthernet", "TenGigabitEthernet", "Port-channel", "Vlan"} {
		if strings.HasPrefix(s, prefix) {
			rest := s[len(prefix):]
			if n, err := strconv.Atoi(rest); err == nil {
				return map[string]any{prefix: n}
			}
			return map[string]any{prefix: rest}
		}
	}
	return map[string]any{"Loopback": s}
}

// interfaceYANGToString converts {"Loopback": 150} → "Loopback150"
func interfaceYANGToString(m map[string]any) string {
	for k, v := range m {
		if isInterfaceType(k) {
			return fmt.Sprintf("%s%v", k, v)
		}
	}
	return ""
}

func isInterfaceType(k string) bool {
	switch k {
	case "Loopback", "GigabitEthernet", "TenGigabitEthernet", "Port-channel",
		"Vlan", "FastEthernet", "TwoGigabitEthernet", "FiveGigabitEthernet",
		"AppGigabitEthernet", "HundredGigE", "FortyGigabitEthernet":
		return true
	}
	return false
}

// vrfEntryToYANG converts netascode VRF entry to YANG format.
func vrfEntryToYANG(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	if vrf, ok := m["vrf"]; ok {
		out["id"] = vrf
	}
	if v, ok := m["autorp"]; ok {
		ac := map[string]any{"autorp": v}
		if al, ok := m["autorp-listener"]; ok {
			if b, ok := al.(bool); ok && b {
				ac["listener"] = []any{nil}
			}
		}
		out["autorp-container"] = ac
	} else if al, ok := m["autorp-listener"]; ok {
		if b, ok := al.(bool); ok && b {
			out["autorp-container"] = map[string]any{"listener": []any{nil}}
		}
	}
	if bsr, ok := m["bsr-candidate"].(map[string]any); ok {
		out["bsr-candidate"] = interfaceToYANG(bsr, "mask-length", "mask")
	}
	if v, ok := m["rp-address"]; ok {
		out["rp-address-conf"] = map[string]any{"address": fmt.Sprintf("%v", v)}
	}
	if v, ok := m["rp-addresses"]; ok {
		out["rp-address-list"] = v
	}
	if v, ok := m["rp-candidate"]; ok {
		out["rp-candidate"] = v
	}
	if v, ok := m["ssm"]; ok {
		out["ssm"] = v
	}
	if v, ok := m["register-source"]; ok {
		out["register-source"] = interfaceStringToYANG(fmt.Sprintf("%v", v))
	}
	return out
}

// vrfEntryFromYANG converts YANG VRF entry to netascode format.
func vrfEntryFromYANG(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch k {
		case "id":
			out["vrf"] = v
		case "autorp-container":
			if ac, ok := v.(map[string]any); ok {
				if a, ok := ac["autorp"]; ok {
					out["autorp"] = a
				}
				if _, ok := ac["listener"]; ok {
					out["autorp-listener"] = true
				}
			}
		case "bsr-candidate":
			if bsr, ok := v.(map[string]any); ok {
				out["bsr-candidate"] = interfaceFromYANG(bsr, "mask", "mask-length")
			}
		case "rp-address-conf":
			if conf, ok := v.(map[string]any); ok {
				if addr, ok := conf["address"]; ok {
					out["rp-address"] = addr
				}
			}
		case "rp-address-list":
			out["rp-addresses"] = v
		case "rp-candidate":
			out["rp-candidate"] = v
		case "ssm":
			out["ssm"] = v
		case "register-source":
			if rs, ok := v.(map[string]any); ok {
				out["register-source"] = interfaceYANGToString(rs)
			}
		default:
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
