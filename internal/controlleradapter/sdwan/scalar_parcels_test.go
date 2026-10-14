// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"encoding/json"
	"testing"
)

func TestScalarParcelMappings(t *testing.T) {
	_, body, e := scalarParcelData("omp", map[string]any{"send_path_limit": float64(8), "holdtime": float64(0), "overlay_as_variable": "overlay_as"})
	if e != nil {
		t.Fatal(e)
	}
	if body["sendPathLimit"].(map[string]any)["value"] != float64(8) {
		t.Fatal("public-to-provider rename lost")
	}
	if body["overlayAs"].(map[string]any)["value"] != "{{overlay_as}}" {
		t.Fatal("variable encoding")
	}
	_, defaults, e := scalarParcelData("omp", map[string]any{})
	if e != nil {
		t.Fatal(e)
	}
	if defaults["sendPathLimit"].(map[string]any)["optionType"] != "default" {
		t.Fatal("mutated shared defaults")
	}
	for _, input := range []map[string]any{{"send_path_limit": float64(-1)}, {"holdtime": float64(1)}, {"unknown": true}, {"overlay_as_variable": "bad value"}, {"graceful_restart": "true"}} {
		if _, _, e := scalarParcelData("omp", input); e == nil {
			t.Fatal("invalid input accepted")
		}
	}
}
func TestGeneratedParcelCoverage(t *testing.T) {
	var definitions struct{ Parcels map[string]scalarDefinition }
	if e := json.Unmarshal(scalarDefinitions, &definitions); e != nil {
		t.Fatal(e)
	}
	if len(definitions.Parcels["global"].Fields) != 27 || len(definitions.Parcels["omp"].Fields) != 30 {
		t.Fatal("generated mapping coverage changed")
	}
}

func TestScalarParcelNestedLists(t *testing.T) {
	_, body, e := scalarParcelData("basic", map[string]any{"affinity_per_vrfs": []any{map[string]any{"affinity_group_number": float64(1), "vrf_range": "1-10"}, map[string]any{"affinity_group_number_variable": "group", "vrf_range": "20"}}})
	if e != nil {
		t.Fatal(e)
	}
	entries := body["affinityPerVrf"].([]any)
	if len(entries) != 2 {
		t.Fatal("list mapping")
	}
	first := entries[0].(map[string]any)["affinityGroupNumber"].(map[string]any)
	second := entries[1].(map[string]any)["affinityGroupNumber"].(map[string]any)
	if first["optionType"] != "global" || second["optionType"] != "variable" {
		t.Fatal("nested defaults shared across entries")
	}
	if _, _, e := scalarParcelData("basic", map[string]any{"affinity_per_vrfs": []any{map[string]any{"name": "ignored"}}}); e == nil {
		t.Fatal("unknown nested field accepted")
	}
}

func TestScalarSchemaCombinators(t *testing.T) {
	schema := map[string]any{"type": "string", "maxLength": float64(3), "oneOf": []any{map[string]any{"pattern": "^[a-z]+$"}, map[string]any{"pattern": "^[0-9]+$"}}}
	if !validScalarValue("abc", schema) || validScalarValue("abcd", schema) || validScalarValue("a1", schema) {
		t.Fatal("oneOf must retain sibling constraints")
	}
	if validScalarValue("not an ip", map[string]any{"format": "ipv6"}) || !validScalarValue("2001:db8::1", map[string]any{"format": "ipv6"}) {
		t.Fatal("IPv6 validation")
	}
}
