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

import "fmt"

// netascode → Cisco-IOS-XE-native YANG body-shape helpers used by the
// interface_* family writers. The netascode data model is
// intentionally flat (a single map of leaf-name → value) but the
// Cisco-IOS-XE-native YANG model nests the address into
// `ip.address.primary` and the VRF binding into `vrf.forwarding`.
//
// Caught against a live Cat9300-24P running IOS-XE 17.18.2: a PUT to
// `/Cisco-IOS-XE-native:native/interface/Loopback=9997` carrying
// `{"ipv4_address":...}` was rejected with
// `unknown-element: ipv4_address`. Five interface writers use the
// flat managed-leaf shape and need this translation to talk to a
// real device:
//
//   interface_loopback, interface_port_channel,
//   interface_virtual_port_group, interface_vlan, interface_tunnel
//
// The transforms are scoped to the leaves that need it. Other leaves
// (description, shutdown, mtu, name, type, id) pass through unchanged
// because the YANG model also keeps those flat. New managed leaves
// added later compose without touching these helpers.

// interfaceIPv4VRFToYANG translates the netascode-flat managed-leaf
// shape into the nested Cisco-IOS-XE-native body the device expects.
//
//	ipv4_address         → ip.address.primary.address
//	ipv4_address_mask    → ip.address.primary.mask
//	vrf                  → vrf.forwarding
//	ip_access_group_in   → ip.access-group.in.acl.acl-name
//	ip_access_group_out  → ip.access-group.out.acl.acl-name
//
// All other leaves pass through. Empty / nil inputs are dropped so
// the body the transport sends does not carry a half-initialized
// nested container.
func interfaceIPv4VRFToYANG(flat map[string]any) map[string]any {
	out := make(map[string]any, len(flat))
	for k, v := range flat {
		switch k {
		case "ipv4_address":
			if v == nil {
				continue
			}
			ensureIPPrimary(out)["address"] = v
		case "ipv4_address_mask":
			if v == nil {
				continue
			}
			ensureIPPrimary(out)["mask"] = v
		case "vrf":
			s, ok := v.(string)
			if !ok || s == "" {
				continue
			}
			out["vrf"] = map[string]any{"forwarding": s}
		case "ip_access_group_in":
			s, ok := v.(string)
			if !ok || s == "" {
				continue
			}
			ensureIPAccessGroup(out, "in")["acl-name"] = s
		case "ip_access_group_out":
			s, ok := v.(string)
			if !ok || s == "" {
				continue
			}
			ensureIPAccessGroup(out, "out")["acl-name"] = s
		case "shutdown":
			// YANG type empty: presence = shut, absence = no shut.
			// RFC 7951 encodes empty leaves as [null].
			// Caught against C8000V 17.16.01a: {"shutdown":false}
			// rejected with invalid-value.
			if isTrue(v) {
				out["shutdown"] = []any{nil}
			}
			// false / nil → omit the leaf entirely (no shutdown)
		default:
			out[k] = v
		}
	}
	return out
}

// interfaceVPGToYANG is the VirtualPortGroup variant. IOS-XE's
// Cisco-IOS-XE-native VPG list is keyed by `name` (string), but the
// netascode shape uses `id` (int) as the per-interface identifier.
// The base interfaceIPv4VRFToYANG passes `id` through verbatim,
// which the device rejects with `missing-element: name`. Caught
// against test 03 retest 2026-04-28.
func interfaceVPGToYANG(flat map[string]any) map[string]any {
	if id, hasID := flat["id"]; hasID {
		dup := make(map[string]any, len(flat))
		for k, v := range flat {
			if k == "id" {
				continue
			}
			dup[k] = v
		}
		dup["name"] = fmt.Sprint(id)
		flat = dup
	}
	return interfaceIPv4VRFToYANG(flat)
}

// interfaceIPv4VRFFromYANG inverts the above so observed-state and
// desired-state share the netascode shape that leavesEqual compares.
// Leaves the device returns inside the nested containers are lifted
// back to the flat keys; leaves we do not model pass through and are
// ignored by leavesEqual's managed-leaf filter.
func interfaceIPv4VRFFromYANG(yang map[string]any) map[string]any {
	out := make(map[string]any, len(yang))
	for k, v := range yang {
		switch k {
		case "ip":
			ip, ok := v.(map[string]any)
			if !ok {
				continue
			}
			if addr, ok := ip["address"].(map[string]any); ok {
				if prim, ok := addr["primary"].(map[string]any); ok {
					if a, ok := prim["address"]; ok {
						out["ipv4_address"] = a
					}
					if m, ok := prim["mask"]; ok {
						out["ipv4_address_mask"] = m
					}
				}
			}
			if ag, ok := ip["access-group"].(map[string]any); ok {
				for _, dir := range []string{"in", "out"} {
					d, ok := ag[dir].(map[string]any)
					if !ok {
						continue
					}
					acl, ok := d["acl"].(map[string]any)
					if !ok {
						continue
					}
					if name, ok := acl["acl-name"]; ok {
						out["ip_access_group_"+dir] = name
					}
				}
			}
		case "vrf":
			vrf, ok := v.(map[string]any)
			if !ok {
				// Already flat (test fixtures) — keep as-is.
				out["vrf"] = v
				continue
			}
			if fwd, ok := vrf["forwarding"]; ok {
				out["vrf"] = fwd
			}
		case "shutdown":
			// YANG empty leaf: presence → true, absence → false.
			// Device returns "shutdown": [null] when the interface
			// is shut; the key is absent when it's not shut.
			out["shutdown"] = true
		default:
			out[k] = v
		}
	}
	// If the device didn't return "shutdown", the interface is not
	// shut — leave the key absent so leavesEqual treats it as equal
	// to a desired map that also omits "shutdown".
	return out
}

// ensureIPAccessGroup lazily creates the nested ip.access-group.<dir>.acl
// map on `out`, returning the inner-most map so callers can write the
// `acl-name` leaf into it. Direction is "in" or "out".
func ensureIPAccessGroup(out map[string]any, dir string) map[string]any {
	ip, ok := out["ip"].(map[string]any)
	if !ok {
		ip = map[string]any{}
		out["ip"] = ip
	}
	ag, ok := ip["access-group"].(map[string]any)
	if !ok {
		ag = map[string]any{}
		ip["access-group"] = ag
	}
	d, ok := ag[dir].(map[string]any)
	if !ok {
		d = map[string]any{}
		ag[dir] = d
	}
	acl, ok := d["acl"].(map[string]any)
	if !ok {
		acl = map[string]any{}
		d["acl"] = acl
	}
	return acl
}

// ensureIPPrimary lazily creates the nested ip.address.primary map
// on `out`, returning the inner-most map so callers can write the
// `address` and `mask` leaves into it.
func ensureIPPrimary(out map[string]any) map[string]any {
	ip, ok := out["ip"].(map[string]any)
	if !ok {
		ip = map[string]any{}
		out["ip"] = ip
	}
	addr, ok := ip["address"].(map[string]any)
	if !ok {
		addr = map[string]any{}
		ip["address"] = addr
	}
	prim, ok := addr["primary"].(map[string]any)
	if !ok {
		prim = map[string]any{}
		addr["primary"] = prim
	}
	return prim
}
