// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package nacmodel

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestSourceYAMLProviderOracle(t *testing.T) {
	b, err := os.ReadFile("testdata/utils-2.0.0-yaml-sources.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string            `json:"name"`
		Inputs   []string          `json:"inputs"`
		Env      map[string]string `json:"env"`
		Expected map[string]any    `json:"expected"`
		Error    bool              `json:"error"`
	}
	if json.Unmarshal(b, &cases) != nil {
		t.Fatal("bad oracle fixtures")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var inputs [][]byte
			for _, s := range c.Inputs {
				inputs = append(inputs, []byte(s))
			}
			raw, _, err := MergeModelSourcesWithOptions(inputs, "all", ModelSourceOptions{ResolveYAML: true, Environment: c.Env})
			if c.Error {
				if err == nil {
					t.Fatal("provider-refused source accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if json.Unmarshal(raw, &got) != nil || !reflect.DeepEqual(got, c.Expected) {
				t.Fatal("source resolution diverged from immutable provider oracle", got, c.Expected)
			}
		})
	}
}

func TestSourceYAMLBindingsAndSnapshot(t *testing.T) {
	t.Setenv("CVK_SOURCE_UNAUTHORIZED", "process-value-not-authorized")
	raw := []byte(`defaults:
  catalyst_center:
    sites:
      floors: &geometry {width: 30, length: 30, height: 3}
catalyst_center:
  sites:
    floors:
      - <<: [*geometry, *geometry]
        name: Floor
        parent_name: !env CVK_SOURCE_PARENT
        floor_number: 1
`)
	options := ModelSourceOptions{ResolveYAML: true, Environment: map[string]string{"CVK_SOURCE_PARENT": "Global/Lab/Building"}}
	_, model, err := MergeModelSourcesWithOptions([][]byte{raw}, "model", options)
	if err != nil {
		t.Fatal(err)
	}
	_, defaults, err := MergeModelSourcesWithOptions([][]byte{raw}, "defaults", options)
	if err != nil || model.InputSnapshotSHA256 != defaults.InputSnapshotSHA256 || model.EnvironmentBindings != 1 || !model.YAMLSourceResolved || model.Resolved || model.ConfigurationParity {
		t.Fatal("invalid resolved-source provenance", err)
	}
	options.Environment["CVK_SOURCE_PARENT"] = "Global/Other/Building"
	_, changed, err := MergeModelSourcesWithOptions([][]byte{raw}, "model", options)
	if err != nil || changed.OrderedInputsSHA256 != model.OrderedInputsSHA256 || changed.InputSnapshotSHA256 == model.InputSnapshotSHA256 {
		t.Fatal("environment change not bound to snapshot", err)
	}
	for _, value := range []string{"!env CVK_SOURCE_UNAUTHORIZED", `"!env CVK_SOURCE_UNAUTHORIZED"`} {
		input := []byte("catalyst_center: {sites: {value: " + value + "}}")
		if _, _, err := MergeModelSourcesWithOptions([][]byte{input}, "model", ModelSourceOptions{ResolveYAML: true}); err == nil {
			t.Fatal("process environment read without explicit binding")
		}
	}
	if _, _, err := MergeModelSourcesWithOptions([][]byte{raw}, "model", ModelSourceOptions{Environment: options.Environment}); err == nil {
		t.Fatal("bindings silently enabled source resolution")
	}
}

func TestSourceYAMLAliasIndependence(t *testing.T) {
	a := []byte(`catalyst_center: {sites: {base: &base {nested: {a: 1}}, first: *base, second: *base}}`)
	b := []byte(`catalyst_center: {sites: {first: {nested: {b: 2}}}}`)
	raw, _, err := MergeModelSourcesWithOptions([][]byte{a, b}, "model", ModelSourceOptions{ResolveYAML: true})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(raw, &got)
	sites := got[Stripe].(map[string]any)["sites"].(map[string]any)
	for _, key := range []string{"base", "second"} {
		nested := sites[key].(map[string]any)["nested"].(map[string]any)
		if len(nested) != 1 || nested["a"] != float64(1) {
			t.Fatal("alias shares mutable merge state")
		}
	}
}

func TestSourceYAMLBoundsAndAmbiguity(t *testing.T) {
	for _, input := range []string{
		"catalyst_center: {sites: {value: &a {self: *a}}}",
		"catalyst_center: {sites: {value: *undefined}}",
		"catalyst_center: {sites: {value: *other}}\n---\ncatalyst_center: {sites: &other {}}",
		"catalyst_center: {sites: {value: 1, value: 2}}",
		`catalyst_center: {sites: {42: a, "42": b}}`,
		"catalyst_center: {sites: {value: .inf}}",
		"catalyst_center: {sites: {value: !custom [a]}}",
	} {
		if _, _, err := MergeModelSourcesWithOptions([][]byte{[]byte(input)}, "model", ModelSourceOptions{ResolveYAML: true}); err == nil {
			t.Fatal("invalid/ambiguous source accepted")
		}
	}
	var source strings.Builder
	source.WriteString("catalyst_center:\n  sites:\n    a0: &a0 [value]\n")
	for i := 1; i < 25; i++ {
		fmt.Fprintf(&source, "    a%d: &a%d [*a%d, *a%d]\n", i, i, i-1, i-1)
	}
	if _, _, err := MergeModelSourcesWithOptions([][]byte{[]byte(source.String())}, "model", ModelSourceOptions{ResolveYAML: true}); err == nil {
		t.Fatal("alias expansion unbounded")
	}
	if _, _, err := MergeModelSourcesWithOptions([][]byte{[]byte(`catalyst_center: {sites: {value: !env LARGE}}`)}, "model", ModelSourceOptions{ResolveYAML: true, Environment: map[string]string{"LARGE": strings.Repeat("x", (1<<20)+1)}}); err == nil {
		t.Fatal("resolved byte limit ignored")
	}
}

func TestSourceYAMLReferencePins(t *testing.T) {
	for name, expected := range map[string]string{"yaml.go.txt": sourceYAMLReferenceSHA256, "resolve_yaml_tags.go.txt": sourceTagReferenceSHA256} {
		raw, err := os.ReadFile("upstream/utils-2.0.0/" + name)
		if err != nil || defaultsDigest(raw) != expected {
			t.Fatal("source reference pin mismatch")
		}
	}
}

func TestSourceYAMLExpandedKeysBoundedBeforeEncoding(t *testing.T) {
	key := strings.Repeat("k", 65536)
	source := "catalyst_center: {sites: {base: &base {" + key + ": value}, copies: [" + strings.Repeat("*base,", 32) + "]}}"
	if _, err := decodeSourceYAML([]byte(source)); err == nil {
		t.Fatal("expanded mapping keys bypassed decoder byte bound")
	}
}

func TestSourceYAMLExpandedTagsBoundedBeforeEncoding(t *testing.T) {
	tag := "!custom" + strings.Repeat("k", 65536)
	source := "catalyst_center: {sites: {base: &base " + tag + " value, copies: [" + strings.Repeat("*base,", 32) + "]}}"
	if _, err := decodeSourceYAML([]byte(source)); err == nil {
		t.Fatal("expanded scalar tag text bypassed decoder byte bound")
	}
}
