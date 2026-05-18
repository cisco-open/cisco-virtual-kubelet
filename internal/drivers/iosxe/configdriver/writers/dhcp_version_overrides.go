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
	"encoding/json"
	"fmt"
	"net"
)

// ──────────────────────────────────────────────────────────────────
// DHCP version-override transforms (IOS-XE 17.x through 17.18, and 26.01)
//
// On these releases, the DHCP pool YANG model differs from the generic
// fallback shape:
//   - List key is "id" (not "name")
//   - Network uses nested: network.primary-network.{number, mask}
//     (not flat network/prefix_length)
//   - default_router → default-router.default-router-list[]
//   - Envelope key is "Cisco-IOS-XE-dhcp:pool" (module prefix required)
//   - Parent container /ip/dhcp must be created before pool PUT
// ──────────────────────────────────────────────────────────────────

// dhcpBodyTransformPre1718 converts the canonical NetAsCode DHCP pool
// body to the id-keyed IOS-XE DHCP YANG shape.
func dhcpBodyTransformPre1718(body map[string]any) map[string]any {
	// Rename name → id.
	if name, ok := body["name"]; ok {
		body["id"] = name
		delete(body, "name")
	}

	// Reshape network + prefix_length → network.primary-network.{number, mask}
	if network, ok := body["network"]; ok {
		if prefix, ok := body["prefix_length"]; ok {
			mask := prefixLenToMask(prefix)
			body["network"] = map[string]any{
				"primary-network": map[string]any{
					"number": network,
					"mask":   mask,
				},
			}
			delete(body, "prefix_length")
		}
	}

	// Reshape default_router → default-router.default-router-list[]
	if dr, ok := body["default_router"]; ok {
		body["default-router"] = map[string]any{
			"default-router-list": []any{dr},
		}
		delete(body, "default_router")
	}

	return body
}

// dhcpFetchTransformPre1718 is the inverse: converts the id-keyed device
// response back to netascode canonical shape for Diff comparison.
func dhcpFetchTransformPre1718(body map[string]any) map[string]any {
	// Rename id → name
	if id, ok := body["id"]; ok {
		body["name"] = id
		delete(body, "id")
	}

	// Flatten primary-network → network + prefix_length
	if netVal, ok := body["network"].(map[string]any); ok {
		if pn, ok := netVal["primary-network"].(map[string]any); ok {
			if num, ok := pn["number"]; ok {
				body["network"] = num
			}
			if mask, ok := pn["mask"]; ok {
				body["prefix_length"] = maskToPrefixLen(mask)
			}
		}
	}

	// Flatten default-router.default-router-list → default_router
	if dr, ok := body["default-router"].(map[string]any); ok {
		if list, ok := dr["default-router-list"].([]any); ok && len(list) > 0 {
			body["default_router"] = list[0]
		}
		delete(body, "default-router")
	}

	return body
}

// prefixLenToMask converts a prefix length (e.g. 24) to a dotted-
// decimal subnet mask (e.g. "255.255.255.0").
func prefixLenToMask(v any) string {
	var bits int
	switch n := v.(type) {
	case int:
		bits = n
	case float64:
		bits = int(n)
	case json.Number:
		i, _ := n.Int64()
		bits = int(i)
	default:
		bits = 24
	}
	if bits < 0 || bits > 32 {
		bits = 24
	}
	mask := net.CIDRMask(bits, 32)
	return fmt.Sprintf("%d.%d.%d.%d", mask[0], mask[1], mask[2], mask[3])
}

// maskToPrefixLen converts a dotted-decimal mask (e.g. "255.255.255.0")
// back to a prefix length (e.g. 24).
func maskToPrefixLen(v any) int {
	s, ok := v.(string)
	if !ok {
		return 24
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return 24
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return 24
	}
	mask := net.IPMask(ip4)
	ones, bits := mask.Size()
	if bits == 0 {
		var count int
		for _, b := range ip4 {
			for i := 7; i >= 0; i-- {
				if b&(1<<uint(i)) != 0 {
					count++
				} else {
					return count
				}
			}
		}
		return count
	}
	return ones
}
