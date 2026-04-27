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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// --- vrf -------------------------------------------------------------------

func TestVRFDiffCreatesMissing(t *testing.T) {
	t.Parallel()
	w := Get("vrf")
	desired := map[string]any{"vrfs": []any{
		map[string]any{"name": "MGMT", "rd": "65000:1"},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if !strings.Contains(ops[0].Path, "=MGMT") {
		t.Errorf("op path=%q, want =MGMT suffix", ops[0].Path)
	}
}

func TestVRFDiffNoChangeWhenEqual(t *testing.T) {
	t.Parallel()
	w := Get("vrf")
	same := map[string]any{"vrfs": []any{
		map[string]any{"name": "MGMT", "rd": "65000:1"},
	}}
	observed := []map[string]any{{"name": "MGMT", "rd": "65000:1"}}
	ops, err := w.Diff(same, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("got %d ops, want 0", len(ops))
	}
}

// --- interface_loopback ----------------------------------------------------

func TestLoopbackDiffCreatesMissing(t *testing.T) {
	t.Parallel()
	w := Get("interface_loopback")
	desired := map[string]any{"interfaces": []any{
		map[string]any{
			"name": "0", "description": "router-id",
			"ipv4_address": "10.255.255.1", "ipv4_address_mask": "255.255.255.255",
		},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
}

// --- interface_virtual_port_group -----------------------------------------

func TestVPGDiffSortsByID(t *testing.T) {
	t.Parallel()
	w := Get("interface_virtual_port_group")
	desired := map[string]any{"interfaces": []any{
		map[string]any{"id": "2", "description": "z"},
		map[string]any{"id": "1", "description": "a"},
	}}
	ops, err := w.Diff(desired, nil)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("got %d ops, want 2", len(ops))
	}
	// Both differ from the empty observed → 2 ops, ordered lex by key.
	if !strings.HasSuffix(ops[0].Path, "=1") || !strings.HasSuffix(ops[1].Path, "=2") {
		t.Errorf("op paths not sorted: %v, %v", ops[0].Path, ops[1].Path)
	}
}

// --- dhcp ------------------------------------------------------------------

func TestDHCPDiffCreatesMissing(t *testing.T) {
	t.Parallel()
	w := Get("dhcp")
	desired := map[string]any{"pools": []any{
		map[string]any{
			"name": "IOX", "network": "192.168.10.0", "prefix_length": 24, "default_router": "192.168.10.1",
		},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 || !strings.Contains(ops[0].Path, "=IOX") {
		t.Fatalf("got %+v, want one op with =IOX", ops)
	}
}

func TestDHCPDiffPreservesDeviceOnlyPools(t *testing.T) {
	t.Parallel()
	w := Get("dhcp")
	desired := map[string]any{"pools": []any{
		map[string]any{"name": "A", "network": "10.0.0.0", "prefix_length": 24, "default_router": "10.0.0.1"},
	}}
	observed := []map[string]any{
		{"name": "A", "network": "10.0.0.0", "prefix_length": float64(24), "default_router": "10.0.0.1"},
		{"name": "B", "network": "10.1.0.0"},
	}
	ops, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("got %+v, want 0 ops (A equal, B not managed)", ops)
	}
}

// --- access_list_extended --------------------------------------------------

func TestACLExtendedDiffOnAddedACL(t *testing.T) {
	t.Parallel()
	w := Get("access_list_extended")
	desired := map[string]any{"extended": []any{
		map[string]any{"name": "IOX-IN", "rules": []any{
			map[string]any{"sequence": 10, "action": "permit"},
		}},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
}

// --- system ----------------------------------------------------------------

func TestSystemDiffOnChangedHostname(t *testing.T) {
	t.Parallel()
	w := Get("system")
	desired := map[string]any{"hostname": "edge-01"}
	observed := map[string]any{"hostname": "old"}
	ops, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].Verb != transport.VerbReplace {
		t.Errorf("verb=%v, want REPLACE", ops[0].Verb)
	}
	var body map[string]any
	if err := json.Unmarshal(ops[0].Body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["Cisco-IOS-XE-native:hostname"] != "edge-01" {
		t.Errorf("body=%v", body)
	}
}

func TestSystemDiffNoChangeOnEqual(t *testing.T) {
	t.Parallel()
	w := Get("system")
	desired := map[string]any{"hostname": "edge-01"}
	observed := map[string]any{"hostname": "edge-01"}
	ops, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("got %d ops, want 0", len(ops))
	}
}

// --- interface_ethernet ----------------------------------------------------

func TestEthernetDiffOnCreate(t *testing.T) {
	t.Parallel()
	w := Get("interface_ethernet")
	desired := map[string]any{"interfaces": []any{
		map[string]any{"type": "GigabitEthernet", "name": "0/0/0", "description": "Uplink"},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if !strings.Contains(ops[0].Path, "GigabitEthernet=0/0/0") {
		t.Errorf("op path=%q", ops[0].Path)
	}
}

func TestEthernetRejectsUnknownType(t *testing.T) {
	t.Parallel()
	w := Get("interface_ethernet")
	desired := map[string]any{"interfaces": []any{
		map[string]any{"type": "CopperBus", "name": "0/0/0"},
	}}
	_, err := w.Diff(desired, []map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("got %v, want unsupported-type error", err)
	}
}

// --- FetchOver httptest (integration smoke for keyed_list helper) ----------

func TestVRFFetchOverHTTPTest(t *testing.T) {
	t.Parallel()
	w := Get("vrf")
	cli, _ := newTestTransport(t, func(wt http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(wt,
			`{"Cisco-IOS-XE-native:definition":[{"name":"MGMT","rd":"65000:1"}]}`)
	})
	got, err := w.Fetch(context.Background(), cli)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	list := got.([]map[string]any)
	if len(list) != 1 || list[0]["name"] != "MGMT" {
		t.Fatalf("decoded=%#v", list)
	}
}

// TestUnwrapYANGEnvelopeAcceptsLocalOnlyKey is a regression test for
// the live-device follow-on finding: NETCONF Fetch (after the RFC
// 7951 xml→json conversion) emits the envelope with the LOCAL-only
// key for same-namespace children (e.g. `{"banner": {...}}`), while
// RESTCONF emits the qualified form (`{"Cisco-IOS-XE-native:banner": {...}}`).
// unwrapYANGEnvelope must accept both shapes so the same writer code
// drives both transports without family-by-family special-casing.
func TestUnwrapYANGEnvelopeAcceptsLocalOnlyKey(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "qualified key matches",
			body: `{"Cisco-IOS-XE-native:banner":{"motd":{"banner":"hi"}}}`,
			want: `{"motd":{"banner":"hi"}}`,
		},
		{
			name: "local-only key matches (RFC 7951 same-namespace)",
			body: `{"banner":{"motd":{"banner":"hi"}}}`,
			want: `{"motd":{"banner":"hi"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := unwrapYANGEnvelope([]byte(tc.body), "Cisco-IOS-XE-native:banner")
			if err != nil {
				t.Fatalf("unwrap: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("unwrap mismatch:\n got:  %s\n want: %s", got, tc.want)
			}
		})
	}
}
