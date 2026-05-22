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

// --- Singleton writers (cdp, lldp, banner, logging, snmp, aaa, bgp) --------

// cdpBody decodes a cdp writer op body and returns the inner
// Cisco-IOS-XE-native:cdp container.
func cdpBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode body %s: %v", raw, err)
	}
	cdp, ok := env["Cisco-IOS-XE-native:cdp"].(map[string]any)
	if !ok {
		t.Fatalf("body %s: missing Cisco-IOS-XE-native:cdp envelope", raw)
	}
	return cdp
}

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
	// The netascode `run` leaf must be emitted as the YANG `run-enable`
	// boolean leaf — the bare `run` leaf is `status obsolete` and is
	// rejected by RESTCONF on 17.15/17.16. Regression guard for #124.
	cdp := cdpBody(t, ops[0].Body)
	if cdp["run-enable"] != true {
		t.Errorf("body=%s, want run-enable:true (boolean)", ops[0].Body)
	}
	if _, has := cdp["run"]; has {
		t.Errorf("body=%s, must not emit the obsolete bare `run` leaf", ops[0].Body)
	}
}

// TestCDPDiffInSyncWhenEnabled confirms the writer reports InSync only
// when CDP is genuinely enabled — the inverse of the #124 false-InSync
// symptom.
func TestCDPDiffInSyncWhenEnabled(t *testing.T) {
	t.Parallel()
	w := Get("cdp")
	same := map[string]any{"run": true}
	ops, err := w.Diff(same, same)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("got %d ops, want 0 (InSync)", len(ops))
	}
}

// TestCDPDiffAdvertiseV2Hyphenated confirms the netascode advertise_v2
// leaf is emitted under its hyphenated YANG name advertise-v2.
func TestCDPDiffAdvertiseV2Hyphenated(t *testing.T) {
	t.Parallel()
	w := Get("cdp")
	desired := map[string]any{"advertise_v2": true}
	ops, err := w.Diff(desired, map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	cdp := cdpBody(t, ops[0].Body)
	if cdp["advertise-v2"] != true {
		t.Errorf("body=%s, want hyphenated advertise-v2:true", ops[0].Body)
	}
	if _, has := cdp["advertise_v2"]; has {
		t.Errorf("body=%s, must not emit the underscore netascode key", ops[0].Body)
	}
}

// TestCDPFetchMapsRunEnableToCanonical confirms the Fetch path lifts
// the device's YANG leaf names back to the netascode canonical shape so
// drift comparison compares like with like.
func TestCDPFetchMapsRunEnableToCanonical(t *testing.T) {
	t.Parallel()
	w := Get("cdp")
	cli, _ := newTestTransport(t, func(wt http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(wt, `{"Cisco-IOS-XE-native:cdp":{"run-enable":true,"advertise-v2":false}}`)
	})
	got, err := w.Fetch(context.Background(), cli)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	m := got.(map[string]any)
	if m["run"] != true {
		t.Errorf("got %#v, want canonical run:true from device run-enable", m)
	}
	if m["advertise_v2"] != false {
		t.Errorf("got %#v, want canonical advertise_v2:false", m)
	}
	if _, has := m["run-enable"]; has {
		t.Errorf("got %#v, YANG run-enable leaked into canonical shape", m)
	}
}

// TestCDPFetchPrefersRunEnableOverObsoleteRun confirms that when a
// device reports both the obsolete `run` empty leaf and the current
// `run-enable` boolean leaf, run-enable is authoritative.
func TestCDPFetchPrefersRunEnableOverObsoleteRun(t *testing.T) {
	t.Parallel()
	w := Get("cdp")
	cli, _ := newTestTransport(t, func(wt http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(wt, `{"Cisco-IOS-XE-native:cdp":{"run":[null],"run-enable":false}}`)
	})
	got, err := w.Fetch(context.Background(), cli)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	m := got.(map[string]any)
	if m["run"] != false {
		t.Errorf("got %#v, want run:false (run-enable wins over obsolete run)", m)
	}
}

// TestCDPDiffLegacyPrefixesAugmentedLeaves confirms that on IOS-XE
// < 17.18 the augmented cdp leaves carry the Cisco-IOS-XE-cdp: module
// prefix in the RESTCONF body — without it the device rejects the
// PATCH. Codex adversarial-review finding on the #124 fix.
func TestCDPDiffLegacyPrefixesAugmentedLeaves(t *testing.T) {
	t.Parallel()
	w := GetForRelease("cdp", "17.16.01a")
	if w == nil {
		t.Fatal("GetForRelease(cdp, 17.16.01a) returned nil")
	}
	desired := map[string]any{"run": true, "advertise_v2": true}
	ops, err := w.Diff(desired, map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	cdp := cdpBody(t, ops[0].Body)
	if cdp["Cisco-IOS-XE-cdp:run-enable"] != true {
		t.Errorf("body=%s, want module-prefixed Cisco-IOS-XE-cdp:run-enable", ops[0].Body)
	}
	if cdp["Cisco-IOS-XE-cdp:advertise-v2"] != true {
		t.Errorf("body=%s, want module-prefixed Cisco-IOS-XE-cdp:advertise-v2", ops[0].Body)
	}
	if _, has := cdp["run-enable"]; has {
		t.Errorf("body=%s, unprefixed run-enable leaks on a < 17.18 device", ops[0].Body)
	}
}

// TestCDPFetchLegacyStripsModulePrefix confirms the Fetch path on a
// < 17.18 device reverses the Cisco-IOS-XE-cdp: prefix back to the
// netascode canonical shape so drift comparison compares like with like.
func TestCDPFetchLegacyStripsModulePrefix(t *testing.T) {
	t.Parallel()
	w := GetForRelease("cdp", "17.16.01a")
	if w == nil {
		t.Fatal("GetForRelease(cdp, 17.16.01a) returned nil")
	}
	cli, _ := newTestTransport(t, func(wt http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(wt, `{"Cisco-IOS-XE-native:cdp":{"Cisco-IOS-XE-cdp:run-enable":true,"Cisco-IOS-XE-cdp:advertise-v2":false}}`)
	})
	got, err := w.Fetch(context.Background(), cli)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	m := got.(map[string]any)
	if m["run"] != true {
		t.Errorf("got %#v, want canonical run:true", m)
	}
	if m["advertise_v2"] != false {
		t.Errorf("got %#v, want canonical advertise_v2:false", m)
	}
}

// TestCDPFetchDecodesObsoleteRunEmptyLeaf confirms a device that
// reports only the obsolete `run` empty leaf ([null]) is decoded to a
// canonical boolean — otherwise leavesEqual compares a bool against a
// slice and reconcile loops on false drift. Codex adversarial-review
// finding on the #124 fix.
func TestCDPFetchDecodesObsoleteRunEmptyLeaf(t *testing.T) {
	t.Parallel()
	w := Get("cdp")
	cli, _ := newTestTransport(t, func(wt http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(wt, `{"Cisco-IOS-XE-native:cdp":{"run":[null]}}`)
	})
	got, err := w.Fetch(context.Background(), cli)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	m := got.(map[string]any)
	if m["run"] != true {
		t.Errorf("got %#v (%T for run), want canonical run:true", m, m["run"])
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
	if !strings.Contains(string(ops[0].Body), `"banner":"new"`) {
		t.Fatalf("body=%s, want banner text container", ops[0].Body)
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
	if !strings.Contains(string(ops[0].Body), `[null]`) {
		t.Fatalf("body=%s, want YANG empty leaf encoding", ops[0].Body)
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
	// New entries MERGE to the parent list path (no =key suffix)
	// so the device creates the entry.
	if len(ops) != 1 || strings.Contains(ops[0].Path, "=") {
		t.Fatalf("ops=%+v, want 1 op to parent path (no =key)", ops)
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
	// New entries MERGE to the parent list path.
	if len(ops) != 1 || strings.Contains(ops[0].Path, "=") {
		t.Fatalf("ops=%+v, want 1 op to parent path (no =key)", ops)
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

func TestLineTransportInputShapesNestedYANG(t *testing.T) {
	t.Parallel()
	w := Get("line")
	desired := map[string]any{"vty": []any{
		map[string]any{"first": 0, "last": 4, "transport": map[string]any{"input": []any{"ssh"}}},
	}}
	ops, err := w.Diff(desired, []map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(string(ops[0].Body), `"input":{"input":["ssh"]}`) {
		t.Fatalf("body=%s, want nested transport input", ops[0].Body)
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
	if !strings.Contains(ops[0].Path, "GigabitEthernet=0%2F0%2F1/switchport") {
		t.Errorf("op path=%q (want %% percent-encoded slashes inside the key value per RFC 8040 §3.5.3.1)", ops[0].Path)
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
