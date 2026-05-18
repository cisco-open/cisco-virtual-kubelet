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
	"reflect"
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
	// New entries MERGE to the parent list path.
	if strings.Contains(ops[0].Path, "=") {
		t.Errorf("op path=%q, want parent path (no =key) for new entry", ops[0].Path)
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
	// Both are new entries → MERGE to parent path.
	if strings.Contains(ops[0].Path, "=") || strings.Contains(ops[1].Path, "=") {
		t.Errorf("op paths should be parent paths for new entries: %v, %v", ops[0].Path, ops[1].Path)
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
	if len(ops) != 1 || ops[0].Path != dhcpParentPath {
		t.Fatalf("got %+v, want one op to DHCP parent", ops)
	}
	var body map[string]map[string][]map[string]any
	if err := json.Unmarshal(ops[0].Body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	pools := body[dhcpParentEnvelopeKey][dhcpPoolEnvelopeKey]
	if len(pools) != 1 || pools[0]["name"] != "IOX" {
		t.Fatalf("body=%s, want baseline DHCP pool keyed by name", ops[0].Body)
	}
}

func TestDHCPDiffLegacyUsesResolverTransform(t *testing.T) {
	t.Parallel()
	w := dhcpWriter{resolver: NewOverrideResolverForMajorMinor(17, 16)}
	desired := map[string]any{"pools": []any{
		map[string]any{
			"name": float64(198181000), "network": "198.18.100.0", "prefix_length": 24, "default_router": "198.18.100.1",
		},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 || ops[0].Path != dhcpParentPath {
		t.Fatalf("got %+v, want one op to DHCP parent", ops)
	}
	var body map[string]map[string][]map[string]any
	if err := json.Unmarshal(ops[0].Body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	pools := body[dhcpParentEnvelopeKey][dhcpPoolEnvelopeKey]
	if len(pools) != 1 || pools[0]["id"] != "198181000" {
		t.Fatalf("body=%s, want legacy DHCP pool keyed by normalized id", ops[0].Body)
	}
	if _, ok := pools[0]["name"]; ok {
		t.Fatalf("body=%s, legacy body must not carry name key", ops[0].Body)
	}
	network, ok := pools[0]["network"].(map[string]any)
	if !ok {
		t.Fatalf("body=%s, want legacy nested network", ops[0].Body)
	}
	primary, ok := network["primary-network"].(map[string]any)
	if !ok || primary["mask"] != "255.255.255.0" {
		t.Fatalf("body=%s, want legacy primary-network mask", ops[0].Body)
	}
}

func TestDHCPDiff2601UsesResolverTransform(t *testing.T) {
	t.Parallel()
	w := dhcpWriter{resolver: NewOverrideResolverForMajorMinor(26, 1)}
	desired := map[string]any{"pools": []any{
		map[string]any{
			"name": "192_168_99.0", "network": "192.168.99.0", "prefix_length": 24, "default_router": "192.168.99.1",
		},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 || ops[0].Path != dhcpParentPath {
		t.Fatalf("got %+v, want one op to DHCP parent", ops)
	}
	var body map[string]map[string][]map[string]any
	if err := json.Unmarshal(ops[0].Body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	pools := body[dhcpParentEnvelopeKey][dhcpPoolEnvelopeKey]
	if len(pools) != 1 || pools[0]["id"] != "192_168_99.0" {
		t.Fatalf("body=%s, want 26.01 DHCP pool keyed by id", ops[0].Body)
	}
	if _, ok := pools[0]["default_router"]; ok {
		t.Fatalf("body=%s, 26.01 body must not carry canonical default_router key", ops[0].Body)
	}
	if dr, ok := pools[0]["default-router"].(map[string]any); !ok {
		t.Fatalf("body=%s, want 26.01 nested default-router", ops[0].Body)
	} else if got := dr["default-router-list"]; !reflect.DeepEqual(got, []any{"192.168.99.1"}) {
		t.Fatalf("default-router-list=%#v, want [192.168.99.1]", got)
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

func TestUsernameSecretTypeShapesToEncryption(t *testing.T) {
	t.Parallel()
	w := Get("username")
	desired := map[string]any{"users": []any{
		map[string]any{"name": "admin", "secret": map[string]any{"type": 9, "secret": "$9$abc"}},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if strings.Contains(string(ops[0].Body), `"type"`) {
		t.Fatalf("body=%s, want type renamed before RESTCONF write", ops[0].Body)
	}
	if !strings.Contains(string(ops[0].Body), `"encryption":"9"`) {
		t.Fatalf("body=%s, want encryption leaf", ops[0].Body)
	}
}

func TestVLANDiffOmitsFalseShutdown(t *testing.T) {
	t.Parallel()
	w := Get("vlan")
	desired := map[string]any{"vlans": []any{
		map[string]any{"id": 99, "name": "VLAN0099", "shutdown": false},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if strings.Contains(string(ops[0].Body), "shutdown") {
		t.Fatalf("body=%s, want false shutdown omitted for YANG empty leaf", ops[0].Body)
	}
}

func TestSpanningTreeFetchStripsPrefixedLeaves(t *testing.T) {
	t.Parallel()
	w := Get("spanning_tree")
	cli, _ := newTestTransport(t, func(wt http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(wt, `{"Cisco-IOS-XE-native:spanning-tree":{"Cisco-IOS-XE-spanning-tree:mode":"rapid-pvst","Cisco-IOS-XE-spanning-tree:extend":{"system-id":[null]}}}`)
	})
	got, err := w.Fetch(context.Background(), cli)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	m := got.(map[string]any)
	if m["mode"] != "rapid-pvst" {
		t.Fatalf("got=%v, want local mode key", got)
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
	// Path percent-encodes "/" inside the key value per RFC 8040 §3.5.3.1.
	if !strings.Contains(ops[0].Path, "GigabitEthernet=0%2F0%2F0") {
		t.Errorf("op path=%q (want percent-encoded slashes inside the key value)", ops[0].Path)
	}
}

func TestEthernetDiffDropsTypeFromBody(t *testing.T) {
	t.Parallel()
	w := Get("interface_ethernet")
	desired := map[string]any{"interfaces": []any{
		map[string]any{
			"type":              "GigabitEthernet",
			"name":              "0/0",
			"vrf":               "Mgmt-vrf",
			"ipv4_address":      "198.51.100.103",
			"ipv4_address_mask": "255.255.255.0",
			"shutdown":          false,
		},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	var body map[string][]map[string]any
	if err := json.Unmarshal(ops[0].Body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	entry := body["Cisco-IOS-XE-native:GigabitEthernet"][0]
	if _, has := entry["type"]; has {
		t.Fatalf("body contains NetAsCode-only type discriminator: %v", entry)
	}
	if _, has := entry["shutdown"]; has {
		t.Fatalf("body contains false empty-leaf shutdown: %v", entry)
	}
}

func TestEthernetFetchFlattensIPv4AndVRF(t *testing.T) {
	t.Parallel()
	w := Get("interface_ethernet")
	cli, _ := newTestTransport(t, func(wt http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/GigabitEthernet") {
			http.NotFound(wt, r)
			return
		}
		_, _ = io.WriteString(wt, `{"Cisco-IOS-XE-native:GigabitEthernet":[{"name":"0/0","vrf":{"forwarding":"Mgmt-vrf"},"ip":{"address":{"primary":{"address":"198.51.100.103","mask":"255.255.255.0"}}}}]}`)
	})
	got, err := w.Fetch(context.Background(), cli)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	list := got.([]map[string]any)
	if len(list) != 1 {
		t.Fatalf("got %#v, want one interface", list)
	}
	entry := list[0]
	if entry["type"] != "GigabitEthernet" || entry["vrf"] != "Mgmt-vrf" || entry["ipv4_address"] != "198.51.100.103" {
		t.Fatalf("got %#v, want flat NetAsCode shape", entry)
	}
}

func TestIPSSHDiffAcceptsBulkMode(t *testing.T) {
	t.Parallel()
	w := Get("ip_ssh")
	desired := map[string]any{"bulk-mode": map[string]any{"window-size": 131072}}
	observed := map[string]any{"bulk-mode": map[string]any{"window-size": float64(131072)}}
	ops, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("got %d ops, want InSync", len(ops))
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
