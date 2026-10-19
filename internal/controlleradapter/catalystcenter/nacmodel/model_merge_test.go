// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package nacmodel

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestModelMergeSources(t *testing.T) {
	input := [][]byte{
		[]byte(`catalyst_center:
  sites:
    areas: [{name: Lab, parent_name: Global}]
    floors: [{name: Floor, parent_name: Global/Lab/B, radio: {a: 1}, height: null}]
  inventory: {devices: [{name: router, role: BORDER}]}
  wireless: {ssid: [a, a]}
  network_settings: {network: {value: 1, flag: true, text: old, null_first: null}}
defaults:
  catalyst_center: {sites: {floors: {height: 3}}}
`),
		[]byte(`catalyst_center:
  sites:
    floors: [{name: Floor, parent_name: Global/Lab/B, radio: {b: 2}}]
  wireless: {ssid: [a, b]}
  network_settings: {network: {value: 0, flag: false, text: '', null_first: null}}
defaults:
  catalyst_center: {sites: {floors: {width: 20}}}
`),
	}
	raw, meta, err := MergeModelSources(input, "all")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if json.Unmarshal(raw, &got) != nil {
		t.Fatal("bad merged output")
	}
	model := got[Stripe].(map[string]any)
	floors := model["sites"].(map[string]any)["floors"].([]any)
	if len(floors) != 1 || len(floors[0].(map[string]any)["radio"].(map[string]any)) != 2 {
		t.Fatal("matching list entry not deep merged")
	}
	if _, present := floors[0].(map[string]any)["height"]; !present {
		t.Fatal("newly inserted null lost")
	}
	network := model["network_settings"].(map[string]any)["network"].(map[string]any)
	if network["value"] != float64(0) || network["flag"] != false || network["text"] != "" {
		t.Fatal("explicit primitive overrides lost")
	}
	if len(model["wireless"].(map[string]any)["ssid"].([]any)) != 4 {
		t.Fatal("primitive duplicate deduplicated")
	}
	if meta.Resolved || meta.ConfigurationParity || len(meta.InputsSHA256) != 2 || meta.UtilsRevision != ModelMergeUtilsRevision {
		t.Fatal("false provenance")
	}
	_, modelMeta, e := MergeModelSources(input, "model")
	if e != nil || modelMeta.OrderedInputsSHA256 != meta.OrderedInputsSHA256 {
		t.Fatal("component input binding lost")
	}
	defaults, defaultMeta, e := MergeModelSources(input, "defaults")
	if e != nil || defaultMeta.OrderedInputsSHA256 != meta.OrderedInputsSHA256 || !bytes.Contains(defaults, []byte(`"width": 20`)) {
		t.Fatal("defaults lost")
	}
	reversed, reverseMeta, e := MergeModelSources([][]byte{input[1], input[0]}, "all")
	if e != nil || bytes.Equal(reversed, raw) || reverseMeta.OrderedInputsSHA256 == meta.OrderedInputsSHA256 {
		t.Fatal("source precedence not reflected")
	}
}

func TestModelMergeAllSections(t *testing.T) {
	var inputs [][]byte
	for _, section := range Sections() {
		b, _ := json.Marshal(map[string]any{Stripe: map[string]any{section: map[string]any{"preserved": true}}})
		inputs = append(inputs, b)
	}
	raw, _, err := MergeModelSources(inputs, "model")
	if err != nil {
		t.Fatal(err)
	}
	n, err := ParseDocument(raw)
	if err != nil || len(n.Content)/2 != 11 {
		t.Fatal("merger dropped model sections")
	}
}

func TestModelMergeRefusal(t *testing.T) {
	for _, raw := range []string{"", "[]", "catalyst_center: null", "other: {}", "catalyst_center: {sites: &a {}}", "catalyst_center: {sites: !env SECRET}", "catalyst_center: {sites: .nan}", "catalyst_center: {sites: 2026-10-19}", "catalyst_center: {sites: {}, sites: {}}", "catalyst_center: {}\n---\ncatalyst_center: {}", "catalyst_center: {sites: {}}\ndefaults: {other: {}}", "catalyst_center: {sites: {}}\ndefaults: {catalyst_center: null}", strings.Repeat("x", (1<<20)+1)} {
		if _, _, err := MergeModelSources([][]byte{[]byte(raw)}, "all"); err == nil {
			t.Fatal("unsupported source accepted")
		}
	}
	valid := []byte(`catalyst_center: {sites: {}}`)
	for _, inputs := range [][][]byte{nil, make([][]byte, 129), {valid, []byte("catalyst_center: !env SECRET")}} {
		if _, _, e := MergeModelSources(inputs, "model"); e == nil {
			t.Fatal("invalid inputs accepted")
		}
	}
	if _, _, e := MergeModelSources([][]byte{valid}, "defaults"); e == nil {
		t.Fatal("missing defaults invented")
	}
	if _, _, e := MergeModelSources([][]byte{valid}, "unknown"); e == nil {
		t.Fatal("unknown component accepted")
	}
}

func TestModelMergeDuplicateListAndNullUpdates(t *testing.T) {
	for _, tc := range []struct {
		a, b  string
		count int
	}{
		{`[{name: a}]`, `[{name: a}, {name: a}]`, 3},
		{`[{name: a}, {name: a}]`, `[{name: a}]`, 3},
		{`[{name: a, x: 1}]`, `[{name: a, x: 2}]`, 2},
		{`[{name: a, x: 1}]`, `[{name: a, y: 2}]`, 1},
		{`[{x: 1}]`, `[{x: 1.0}]`, 2},
	} {
		a := []byte("catalyst_center: {inventory: {members: " + tc.a + ", value: 5}}")
		b := []byte("catalyst_center: {inventory: {members: " + tc.b + ", value: null}}")
		raw, _, e := MergeModelSources([][]byte{a, b}, "model")
		if e != nil {
			t.Fatal(e)
		}
		var got map[string]any
		json.Unmarshal(raw, &got)
		inv := got[Stripe].(map[string]any)["inventory"].(map[string]any)
		if len(inv["members"].([]any)) != tc.count || inv["value"] != float64(5) {
			t.Fatal("upstream list/null semantics diverged")
		}
	}
}
