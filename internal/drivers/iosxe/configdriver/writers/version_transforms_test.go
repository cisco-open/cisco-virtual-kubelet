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
	"reflect"
	"testing"
)

// ── prefix_list transforms ──────────────────────────────────────

// Golden data captured from C8000V 17.16.01a RESTCONF GET
// /Cisco-IOS-XE-native:native/ip/prefix-lists.
var prefixListYANG1716Golden = map[string]any{
	"prefixes": []any{
		map[string]any{"name": "PL1", "no": float64(10), "action": "permit", "ip": "10.0.0.0/8", "le": float64(32)},
		map[string]any{"name": "PL1", "no": float64(20), "action": "deny", "ip": "0.0.0.0/0"},
		map[string]any{"name": "PL2", "no": float64(10), "action": "permit", "ip": "192.168.0.0/16"},
	},
	"prefix-list-description": []any{
		map[string]any{"name": "PL1", "description": "allow private"},
	},
}

var prefixListNetascodeGolden = []map[string]any{
	{
		"name":        "PL1",
		"description": "allow private",
		"sequences": []any{
			map[string]any{"no": float64(10), "action": "permit", "ip": "10.0.0.0/8", "le": float64(32)},
			map[string]any{"no": float64(20), "action": "deny", "ip": "0.0.0.0/0"},
		},
	},
	{
		"name": "PL2",
		"sequences": []any{
			map[string]any{"no": float64(10), "action": "permit", "ip": "192.168.0.0/16"},
		},
	},
}

func TestPrefixListFromYANG1716(t *testing.T) {
	t.Parallel()
	got := prefixListFromYANG1716(prefixListYANG1716Golden)
	if !reflect.DeepEqual(got, prefixListNetascodeGolden) {
		t.Fatalf("fromYANG mismatch\n got=%#v\nwant=%#v", got, prefixListNetascodeGolden)
	}
}

func TestPrefixListToYANG1716(t *testing.T) {
	t.Parallel()
	got := prefixListToYANG1716(prefixListNetascodeGolden)
	// Compare via JSON for stable key ordering.
	gotJSON := mustJSON(t, got)
	wantJSON := mustJSON(t, prefixListYANG1716Golden)
	if gotJSON != wantJSON {
		t.Fatalf("toYANG mismatch\n got=%s\nwant=%s", gotJSON, wantJSON)
	}
}

func TestPrefixListRoundTrip(t *testing.T) {
	t.Parallel()
	yang := prefixListToYANG1716(prefixListNetascodeGolden)
	back := prefixListFromYANG1716(yang)
	if !reflect.DeepEqual(back, prefixListNetascodeGolden) {
		t.Fatalf("round-trip mismatch\norig=%#v\nback=%#v", prefixListNetascodeGolden, back)
	}
}

func TestPrefixListToYANG1716_ResolverFixedInput(t *testing.T) {
	t.Parallel()
	// The resolver's FixYAML11BoolKeys has already renamed "false" → "no".
	// Verify the writer handles pre-fixed input correctly.
	fixed := []map[string]any{{
		"name": "PL1",
		"sequences": []any{
			map[string]any{"no": float64(10), "action": "permit", "ip": "10.0.0.0/8"},
		},
	}}
	got := prefixListToYANG1716(fixed)
	prefixes, ok := got["prefixes"].([]any)
	if !ok || len(prefixes) == 0 {
		t.Fatal("expected prefixes in output")
	}
	first := prefixes[0].(map[string]any)
	if v, ok := first["no"]; !ok || v != float64(10) {
		t.Errorf("expected no=10, got %v", v)
	}
}

func TestPrefixListsEqual_MatchingKeys(t *testing.T) {
	t.Parallel()
	// Both sides have "no" (resolver already fixed the keys).
	desired := []map[string]any{{
		"name": "PL1",
		"sequences": []any{
			map[string]any{"no": float64(10), "action": "permit"},
		},
	}}
	observed := []map[string]any{{
		"name": "PL1",
		"sequences": []any{
			map[string]any{"no": float64(10), "action": "permit"},
		},
	}}
	if !prefixListsEqual(desired, observed) {
		t.Error("expected equal with matching keys")
	}
}

func TestPrefixListToYANG1716_DropsNilAndFalse(t *testing.T) {
	t.Parallel()
	input := []map[string]any{{
		"name": "PL1",
		"sequences": []any{
			map[string]any{"no": float64(10), "action": "permit", "ip": "10.0.0.0/8", "ge": false, "le": nil},
		},
	}}
	got := prefixListToYANG1716(input)
	first := got["prefixes"].([]any)[0].(map[string]any)
	if _, ok := first["ge"]; ok {
		t.Error("ge:false should be dropped")
	}
	if _, ok := first["le"]; ok {
		t.Error("le:nil should be dropped")
	}
}

// ── ip_community_list transforms ────────────────────────────────

func TestCommunityListStdToYANG1716(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"standard": []any{
			map[string]any{
				"name": "COMM1",
				"community-list-entry": []any{
					map[string]any{"action": "permit", "community": "65000:500"},
					map[string]any{"action": "deny", "community": "65001:100"},
				},
			},
		},
	}
	got := communityListToYANG1716(input)
	std := got["standard"].([]any)
	if len(std) != 1 {
		t.Fatalf("expected 1 standard entry, got %d", len(std))
	}
	entry := std[0].(map[string]any)
	if entry["name"] != "COMM1" {
		t.Errorf("name = %v, want COMM1", entry["name"])
	}
	permit := entry["permit"].(map[string]any)["permit-list"].([]any)
	if len(permit) != 1 {
		t.Fatalf("expected 1 permit, got %d", len(permit))
	}
	// 65000<<16 | 500 = 4259840500
	if permit[0] != float64(4259840500) {
		t.Errorf("permit[0] = %v, want 4259840500", permit[0])
	}
	deny := entry["deny"].(map[string]any)["deny-list"].([]any)
	// 65001<<16 | 100 = 4259905636
	if deny[0] != float64(4259905636) {
		t.Errorf("deny[0] = %v, want 4259905636", deny[0])
	}
}

func TestCommunityListStdFromYANG1716(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"standard": []any{
			map[string]any{
				"name":   "COMM1",
				"permit": map[string]any{"permit-list": []any{float64(4259840500)}},
				"deny":   map[string]any{"deny-list": []any{float64(4259905636)}},
			},
		},
	}
	got := communityListFromYANG1716(input)
	std := got["standard"].([]any)
	entry := std[0].(map[string]any)
	cle := entry["community-list-entry"].([]any)
	if len(cle) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(cle))
	}
	first := cle[0].(map[string]any)
	if first["action"] != "permit" || first["community"] != "65000:500" {
		t.Errorf("first entry = %v, want permit 65000:500", first)
	}
	second := cle[1].(map[string]any)
	if second["action"] != "deny" || second["community"] != "65001:100" {
		t.Errorf("second entry = %v, want deny 65001:100 (numeric 4259905636)", second)
	}
}

func TestCommunityListExpToYANG1716(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"expanded": []any{
			map[string]any{
				"name": "EXP1",
				"community-list-entry": []any{
					map[string]any{"action": "permit", "regexp": "^65000:.*"},
				},
			},
		},
	}
	got := communityListToYANG1716(input)
	exp := got["expanded"].([]any)
	entry := exp[0].(map[string]any)
	eg := entry["extended-grouping"].(map[string]any)
	inner := eg["extended_grouping"].([]any)
	if len(inner) != 1 {
		t.Fatalf("expected 1 inner, got %d", len(inner))
	}
	rule := inner[0].(map[string]any)
	if rule["action"] != "permit" || rule["string"] != "^65000:.*" {
		t.Errorf("rule = %v", rule)
	}
}

func TestCommunityListExpFromYANG1716(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"expanded": []any{
			map[string]any{
				"name": "EXP1",
				"extended-grouping": map[string]any{
					"extended_grouping": []any{
						map[string]any{"action": "permit", "string": "^65000:.*"},
					},
				},
			},
		},
	}
	got := communityListFromYANG1716(input)
	exp := got["expanded"].([]any)
	entry := exp[0].(map[string]any)
	cle := entry["community-list-entry"].([]any)
	rule := cle[0].(map[string]any)
	if rule["action"] != "permit" || rule["regexp"] != "^65000:.*" {
		t.Errorf("rule = %v", rule)
	}
}

func TestCommunityListRoundTrip(t *testing.T) {
	t.Parallel()
	nac := map[string]any{
		"standard": []any{
			map[string]any{
				"name": "COMM1",
				"community-list-entry": []any{
					map[string]any{"action": "permit", "community": "65000:500"},
				},
			},
		},
		"expanded": []any{
			map[string]any{
				"name": "EXP1",
				"community-list-entry": []any{
					map[string]any{"action": "deny", "regexp": "^65001:"},
				},
			},
		},
	}
	yang := communityListToYANG1716(nac)
	back := communityListFromYANG1716(yang)
	if !reflect.DeepEqual(nac, back) {
		t.Fatalf("round-trip mismatch\norig=%#v\nback=%#v", nac, back)
	}
}

// ── community string ↔ numeric ──────────────────────────────────

func TestCommunityStringToNum(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want any
	}{
		{"65000:500", float64(4259840500)},
		{"0:0", float64(0)},
		{"65535:65535", float64(4294967295)},
		{"no-advertise", "no-advertise"}, // well-known name
		{"bad", "bad"},
	}
	for _, tc := range cases {
		got := communityStringToNum(tc.in)
		if got != tc.want {
			t.Errorf("communityStringToNum(%q) = %v (%T), want %v (%T)",
				tc.in, got, got, tc.want, tc.want)
		}
	}
}

func TestCommunityNumToString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   any
		want string
	}{
		{float64(4259840500), "65000:500"},
		{float64(0), "0:0"},
		{float64(4294967295), "65535:65535"},
		{int(4259840500), "65000:500"},
		{int64(4259840500), "65000:500"},
	}
	for _, tc := range cases {
		got := communityNumToString(tc.in)
		if got != tc.want {
			t.Errorf("communityNumToString(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ── BGP version-branched Diff ───────────────────────────────────

func TestBGPDiffLegacy(t *testing.T) {
	resetVersion(t)
	SetDeviceVersion("17.16.01a")

	w := bgpWriter{}
	desired := map[string]any{
		"id": float64(65000),
		"bgp": map[string]any{
			"router-id": "10.255.255.1",
		},
	}
	ops, err := w.Diff(desired, map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].Path != "/Cisco-IOS-XE-native:native/router" {
		t.Errorf("path = %q, want /Cisco-IOS-XE-native:native/router", ops[0].Path)
	}
	// Body should contain the list wrapper.
	var body map[string]any
	if err := json.Unmarshal(ops[0].Body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	router := body["Cisco-IOS-XE-native:router"].(map[string]any)
	bgpList := router["Cisco-IOS-XE-bgp:bgp"].([]any)
	if len(bgpList) != 1 {
		t.Fatalf("expected 1 BGP list entry, got %d", len(bgpList))
	}
	entry := bgpList[0].(map[string]any)
	if entry["id"] != float64(65000) {
		t.Errorf("id = %v, want 65000", entry["id"])
	}
}

func TestBGPDiff1718(t *testing.T) {
	resetVersion(t)
	SetDeviceVersion("17.18.2")

	w := bgpWriter{}
	desired := map[string]any{
		"id": float64(65000),
		"bgp": map[string]any{
			"router-id": "10.255.255.1",
		},
	}
	ops, err := w.Diff(desired, map[string]any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	if ops[0].Path != bgpYANGPath {
		t.Errorf("path = %q, want %q", ops[0].Path, bgpYANGPath)
	}
	// On 17.18, new BGP → VerbReplace (PUT).
	if ops[0].Verb != "REPLACE" {
		t.Errorf("verb = %q, want REPLACE (create on empty device)", ops[0].Verb)
	}
}

func TestBGPDiffNoChange(t *testing.T) {
	resetVersion(t)
	SetDeviceVersion("17.18.2")

	w := bgpWriter{}
	data := map[string]any{
		"id":  float64(65000),
		"bgp": map[string]any{"router-id": "10.255.255.1"},
	}
	ops, err := w.Diff(data, data)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("got %d ops, want 0 (no change)", len(ops))
	}
}

// Note: fixYAMLNoKey was removed from the writers package. The YAML 1.1
// boolean key fix is now applied globally in the resolver via
// intent.FixYAML11BoolKeys. Tests for that function live in
// intent/yaml11_fix_test.go.

// ── SNMP body transform ─────────────────────────────────────────

func TestSNMPBodyTransform1716(t *testing.T) {
	t.Parallel()
	body := map[string]any{
		"community": []any{
			map[string]any{"name": "public", "RO": []any{nil}},
		},
		"location": "LAB",
	}
	got := snmpBodyTransform1716(body)
	if _, ok := got["community"]; ok {
		t.Error("old 'community' key should be removed")
	}
	configs, ok := got["Cisco-IOS-XE-snmp:community-config"].([]any)
	if !ok || len(configs) != 1 {
		t.Fatalf("expected 1 community-config, got %v", got)
	}
	cfg := configs[0].(map[string]any)
	if cfg["name"] != "public" || cfg["permission"] != "ro" {
		t.Errorf("config = %v, want name=public permission=ro", cfg)
	}
}

// ── logging body transform ──────────────────────────────────────

func TestLoggingBodyTransform1716(t *testing.T) {
	resetVersion(t)
	ResolveForVersion(17, 16)
	o := GetOverride("logging")
	if o == nil || o.BodyTransform == nil {
		t.Fatal("expected logging override with BodyTransform on 17.16")
	}
	body := map[string]any{
		"buffered": map[string]any{"size": float64(8192)},
	}
	got := o.BodyTransform(body)
	buf := got["buffered"].(map[string]any)
	if _, ok := buf["size"]; ok {
		t.Error("'size' should be renamed to 'size-value'")
	}
	if buf["size-value"] != float64(8192) {
		t.Errorf("size-value = %v, want 8192", buf["size-value"])
	}
}

// ── IsLegacyVersion / table-driven path resolution ──────────────

func TestIsLegacyVersion(t *testing.T) {
	resetVersion(t)

	// On 17.16, all 3 custom writers should report legacy.
	SetDeviceVersion("17.16.01a")
	for _, fam := range []string{"bgp", "prefix_list", "ip_community_list"} {
		if !IsLegacyVersion(fam) {
			t.Errorf("IsLegacyVersion(%q) = false on 17.16, want true", fam)
		}
	}

	// On 17.18, none should.
	SetDeviceVersion("17.18.2")
	for _, fam := range []string{"bgp", "prefix_list", "ip_community_list"} {
		if IsLegacyVersion(fam) {
			t.Errorf("IsLegacyVersion(%q) = true on 17.18, want false", fam)
		}
	}
}

func TestResolvedYANGPathFromTable(t *testing.T) {
	resetVersion(t)

	SetDeviceVersion("17.16.01a")
	if got := ResolvedYANGPath("bgp", bgpYANGPath); got != bgpYANGPathLegacy {
		t.Errorf("bgp path on 17.16 = %q, want %q", got, bgpYANGPathLegacy)
	}
	if got := ResolvedYANGPath("prefix_list", prefixListYANGPath1718); got != prefixListYANGPath1716 {
		t.Errorf("prefix_list path on 17.16 = %q, want %q", got, prefixListYANGPath1716)
	}

	SetDeviceVersion("17.18.2")
	if got := ResolvedYANGPath("bgp", bgpYANGPath); got != bgpYANGPath {
		t.Errorf("bgp path on 17.18 = %q, want %q (baseline)", got, bgpYANGPath)
	}
	if got := ResolvedYANGPath("prefix_list", prefixListYANGPath1718); got != prefixListYANGPath1718 {
		t.Errorf("prefix_list path on 17.18 = %q, want %q (baseline)", got, prefixListYANGPath1718)
	}
}

// ── helpers ─────────────────────────────────────────────────────

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(b)
}
