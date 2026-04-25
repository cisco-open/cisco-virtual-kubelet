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
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// --- Singleton writers (cdp, lldp, banner, logging, snmp, aaa, bgp) --------

func TestCDPDiffOnTransition(t *testing.T) {
	t.Parallel()
	w := Get("cdp")
	desired := map[string]any{"run": true}
	observed := map[string]any{"run": false}
	ops, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].Verb != transport.VerbMerge {
		t.Errorf("verb=%v, want MERGE", ops[0].Verb)
	}
}

func TestLLDPDiffNoChangeOnEqual(t *testing.T) {
	t.Parallel()
	w := Get("lldp")
	same := map[string]any{"run": true, "timer": float64(30)}
	if ops, err := w.Diff(same, same); err != nil || len(ops) != 0 {
		t.Fatalf("ops=%v err=%v", ops, err)
	}
}

func TestBannerDiffChangesSelectedLeaf(t *testing.T) {
	t.Parallel()
	w := Get("banner")
	desired := map[string]any{"login": "new", "motd": "same"}
	observed := map[string]any{"login": "old", "motd": "same"}
	ops, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
}

func TestLoggingDiffIgnoresUnmanagedLeaves(t *testing.T) {
	t.Parallel()
	// Device returns leaves we don't model; writer must not treat them
	// as drift.
	w := Get("logging")
	desired := map[string]any{"buffered": float64(50000)}
	observed := map[string]any{"buffered": float64(50000), "exotic-leaf": "present"}
	if ops, err := w.Diff(desired, observed); err != nil || len(ops) != 0 {
		t.Fatalf("ops=%v err=%v", ops, err)
	}
}

func TestSNMPServerFetchEmptyOn404(t *testing.T) {
	t.Parallel()
	w := Get("snmp_server")
	cli, _ := newTestTransport(t, func(wt http.ResponseWriter, r *http.Request) {
		http.Error(wt, "not found", http.StatusNotFound)
	})
	got, err := w.Fetch(context.Background(), cli)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	m := got.(map[string]any)
	if len(m) != 0 {
		t.Fatalf("got %#v, want empty map on 404", m)
	}
}

func TestAAADiffOnChangedLeaf(t *testing.T) {
	t.Parallel()
	w := Get("aaa")
	desired := map[string]any{"new-model": true}
	observed := map[string]any{"new-model": false}
	ops, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
}

func TestBGPDiffOnManagedSubtree(t *testing.T) {
	t.Parallel()
	w := Get("bgp")
	desired := map[string]any{"id": "65000", "bgp": map[string]any{"log-neighbor-changes": true}}
	observed := map[string]any{"id": "65000"}
	ops, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
}

// --- Keyed-list writers (ntp, access_list_standard, static_route, etc.) ---

func TestNTPServersDiffCreatesMissing(t *testing.T) {
	t.Parallel()
	w := Get("ntp")
	desired := map[string]any{"servers": []any{
		map[string]any{"name": "192.0.2.1", "prefer": true},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 || !strings.Contains(ops[0].Path, "=192.0.2.1") {
		t.Fatalf("ops=%+v, want 1 op keyed on 192.0.2.1", ops)
	}
}

func TestStaticRouteDiffByPrefix(t *testing.T) {
	t.Parallel()
	w := Get("static_route")
	desired := map[string]any{"routes": []any{
		map[string]any{"prefix": "0.0.0.0", "mask": "0.0.0.0", "distance": 1},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 || !strings.Contains(ops[0].Path, "=0.0.0.0") {
		t.Fatalf("ops=%+v", ops)
	}
}

func TestPrefixListDiffByName(t *testing.T) {
	t.Parallel()
	w := Get("prefix_list")
	desired := map[string]any{"prefixes": []any{
		map[string]any{"name": "ONLY-DEFAULT"},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops", len(ops))
	}
}

func TestRouteMapDiffByName(t *testing.T) {
	t.Parallel()
	w := Get("route_map")
	desired := map[string]any{"route_maps": []any{
		map[string]any{"name": "IN", "description": "customer"},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops", len(ops))
	}
}

func TestLineDiffByFirst(t *testing.T) {
	t.Parallel()
	w := Get("line")
	desired := map[string]any{"vty": []any{
		map[string]any{"first": 0, "last": 4, "login": "authentication default"},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops", len(ops))
	}
}

func TestACLStandardDiff(t *testing.T) {
	t.Parallel()
	w := Get("access_list_standard")
	desired := map[string]any{"standard": []any{
		map[string]any{"name": "MGMT", "rules": []any{}},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops", len(ops))
	}
}

func TestOSPFDiffByProcessID(t *testing.T) {
	t.Parallel()
	w := Get("ospf")
	desired := map[string]any{"processes": []any{
		map[string]any{"id": 1, "router-id": "10.255.255.1"},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 || !strings.Contains(ops[0].Path, "=1") {
		t.Fatalf("ops=%+v", ops)
	}
}

// --- interface_switchport (composite-key overlay) -------------------------

func TestSwitchportDiffOnCreate(t *testing.T) {
	t.Parallel()
	w := Get("interface_switchport")
	desired := map[string]any{"interfaces": []any{
		map[string]any{"type": "GigabitEthernet", "name": "0/0/1", "mode": "access"},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if !strings.Contains(ops[0].Path, "GigabitEthernet=0/0/1/switchport") {
		t.Errorf("op path=%q", ops[0].Path)
	}
}

func TestSwitchportFetchProjectsSubContainer(t *testing.T) {
	t.Parallel()
	w := Get("interface_switchport")
	// Serve one ethernet subtree with a single interface carrying a
	// switchport block; all other types return 404.
	cli, _ := newTestTransport(t, func(wt http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/GigabitEthernet") {
			_, _ = io.WriteString(wt,
				`{"Cisco-IOS-XE-native:GigabitEthernet":[{"name":"0/0/1","switchport":{"mode":"access"}}]}`)
			return
		}
		http.Error(wt, "not found", http.StatusNotFound)
	})
	got, err := w.Fetch(context.Background(), cli)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	list, _ := got.([]map[string]any)
	if len(list) != 1 {
		t.Fatalf("projected list=%#v", list)
	}
	if list[0]["type"] != "GigabitEthernet" || list[0]["name"] != "0/0/1" {
		t.Errorf("row=%#v", list[0])
	}
}

func TestSwitchportRejectsUnknownType(t *testing.T) {
	t.Parallel()
	w := Get("interface_switchport")
	desired := map[string]any{"interfaces": []any{
		map[string]any{"type": "CopperBus", "name": "0/0/1"},
	}}
	_, err := w.Diff(desired, []map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("got %v, want unsupported-type error", err)
	}
}
