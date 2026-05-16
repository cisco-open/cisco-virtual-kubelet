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
	"reflect"
	"testing"
)

// TestInterfaceIPv4VRFToYANGShape covers the netascode→YANG body
// translation the device requires. Reproducer for the live-device
// finding against a Cat9300-24P running IOS-XE 17.18.2: the writer
// was sending `{"ipv4_address": ...}` and the device returned
// `unknown-element: ipv4_address`.
func TestInterfaceIPv4VRFToYANGShape(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want map[string]any
	}{
		{
			name: "loopback with description + ipv4 + vrf",
			in: map[string]any{
				"name":              9997.0,
				"description":       "ut",
				"ipv4_address":      "10.255.255.97",
				"ipv4_address_mask": "255.255.255.255",
				"vrf":               "MGMT",
			},
			want: map[string]any{
				"name":        9997.0,
				"description": "ut",
				"ip": map[string]any{
					"address": map[string]any{
						"primary": map[string]any{
							"address": "10.255.255.97",
							"mask":    "255.255.255.255",
						},
					},
				},
				"vrf": map[string]any{"forwarding": "MGMT"},
			},
		},
		{
			name: "ipv4 address only (no mask) — primary still nested",
			in: map[string]any{
				"name":         "Tu1",
				"ipv4_address": "10.0.0.1",
			},
			want: map[string]any{
				"name": "Tu1",
				"ip": map[string]any{
					"address": map[string]any{
						"primary": map[string]any{"address": "10.0.0.1"},
					},
				},
			},
		},
		{
			name: "empty vrf is dropped",
			in:   map[string]any{"name": 1.0, "vrf": ""},
			want: map[string]any{"name": 1.0},
		},
		{
			name: "non-string vrf is dropped (defensive)",
			in:   map[string]any{"name": 1.0, "vrf": 42},
			want: map[string]any{"name": 1.0},
		},
		{
			name: "passes through unrelated leaves untouched",
			in: map[string]any{
				"name":        "Po10",
				"description": "uplink",
				"shutdown":    false,
				"mtu":         1500.0,
			},
			want: map[string]any{
				"name":        "Po10",
				"description": "uplink",
				// shutdown: false → omitted (YANG empty leaf absent = no shutdown)
				"mtu": 1500.0,
			},
		},
		{
			name: "shutdown true becomes empty leaf",
			in: map[string]any{
				"name":     "Gi0/0",
				"shutdown": true,
			},
			want: map[string]any{
				"name":     "Gi0/0",
				"shutdown": []any{nil},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := interfaceIPv4VRFToYANG(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("toYANG mismatch\n got=%#v\nwant=%#v", got, tc.want)
			}
		})
	}
}

// TestInterfaceIPv4VRFFromYANGShape covers the inverse used by
// Fetch. Observed-state must end up flat so leavesEqual compares
// against the flat desired-state without per-family branching.
func TestInterfaceIPv4VRFFromYANGShape(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want map[string]any
	}{
		{
			name: "device returns nested ip + vrf containers",
			in: map[string]any{
				"name":        9997.0,
				"description": "ut",
				"ip": map[string]any{
					"address": map[string]any{
						"primary": map[string]any{
							"address": "10.255.255.97",
							"mask":    "255.255.255.255",
						},
					},
				},
				"vrf": map[string]any{"forwarding": "MGMT"},
			},
			want: map[string]any{
				"name":              9997.0,
				"description":       "ut",
				"ipv4_address":      "10.255.255.97",
				"ipv4_address_mask": "255.255.255.255",
				"vrf":               "MGMT",
			},
		},
		{
			name: "primary present but missing leaves",
			in: map[string]any{
				"name": 1.0,
				"ip":   map[string]any{"address": map[string]any{"primary": map[string]any{}}},
			},
			want: map[string]any{"name": 1.0},
		},
		{
			name: "no ip / vrf containers",
			in:   map[string]any{"name": "Po10", "shutdown": true},
			want: map[string]any{"name": "Po10", "shutdown": true},
		},
		{
			name: "ip container malformed — pass through ignored, no panic",
			in:   map[string]any{"name": 1.0, "ip": "not-an-object"},
			want: map[string]any{"name": 1.0},
		},
		{
			name: "vrf already flat (test fixture path)",
			in:   map[string]any{"name": 1.0, "vrf": "MGMT"},
			want: map[string]any{"name": 1.0, "vrf": "MGMT"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := interfaceIPv4VRFFromYANG(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("fromYANG mismatch\n got=%#v\nwant=%#v", got, tc.want)
			}
		})
	}
}

// TestInterfaceIPv4VRFRoundTrip checks that a flat netascode entry
// round-trips through toYANG → fromYANG and lands back at the
// original flat shape. This is the property leavesEqual depends on:
// observed-state (post-fromYANG) must be byte-equal to the matching
// portion of the desired-state (flat) so the reconciler does not
// see phantom drift after a successful apply.
func TestInterfaceIPv4VRFRoundTrip(t *testing.T) {
	// shutdown:false is omitted from toYANG (nothing sent to device),
	// and fromYANG no longer synthesises it, so the round-trip for an
	// explicitly-false shutdown is not bijective. Test the common case
	// where shutdown is absent.
	flat := map[string]any{
		"name":              9997.0,
		"description":       "ut",
		"ipv4_address":      "10.255.255.97",
		"ipv4_address_mask": "255.255.255.255",
		"vrf":               "MGMT",
	}
	roundTripped := interfaceIPv4VRFFromYANG(interfaceIPv4VRFToYANG(flat))
	if !reflect.DeepEqual(flat, roundTripped) {
		t.Fatalf("round-trip mismatch\norig=%#v\nback=%#v", flat, roundTripped)
	}
}
