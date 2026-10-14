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

func TestSecurityNestedRequiredFields(t *testing.T) {
	input := map[string]any{"key_chains": []any{map[string]any{"name": "CVK-chain", "key_id": float64(10)}}}
	_, data, err := scalarParcelData("security", input)
	if err != nil {
		t.Fatal(err)
	}
	chains := data["keychain"].([]any)
	chain := chains[0].(map[string]any)
	if chain["name"].(map[string]any)["value"] != "CVK-chain" {
		t.Fatal("nested name lost as metadata")
	}
	for _, invalid := range []map[string]any{
		{"key_chains": []any{map[string]any{"key_id": float64(10)}}},
		{"keys": []any{map[string]any{"key_chain_name": "chain"}}},
		{"anti_replay_window": float64(512)},
		{"extended_anti_replay_window": false},
	} {
		if _, _, err := scalarParcelData("security", invalid); err == nil {
			t.Fatal("invalid security input accepted")
		}
	}
}
func TestGPSModuleDefaultAndUCSERequiredFields(t *testing.T) {
	_, data, err := scalarParcelData("transport_gps", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if data["enable"].(map[string]any)["value"] != true || data["enable"].(map[string]any)["optionType"] != "global" {
		t.Fatal("NaC GPS default missing")
	}
	if _, _, err := scalarParcelData("other_ucse", map[string]any{"slot": float64(1)}); err == nil {
		t.Fatal("missing bay accepted")
	}
	if _, _, err := scalarParcelData("other_ucse", map[string]any{"slot": float64(1), "bay": float64(0)}); err != nil {
		t.Fatal(err)
	}
}

func TestThousandEyesWrapperAndChoices(t *testing.T) {
	_, data, err := scalarParcelData("other_thousandeyes", map[string]any{"account_group_token_variable": "te_token", "proxy_type": "none", "name_server_ip": "192.0.2.53"})
	if err != nil {
		t.Fatal(err)
	}
	app := data["virtualApplication"].([]any)[0].(map[string]any)
	if app["token"].(map[string]any)["value"] != "{{te_token}}" {
		t.Fatal("wrapper variable mapping lost")
	}
	for _, bad := range []map[string]any{{"proxy_type": "wrong"}, {"name_server_ip": "192.0.2.999"}, {"unsupported": true}} {
		if _, _, err := scalarParcelData("other_thousandeyes", bad); err == nil {
			t.Fatal("invalid wrapper input accepted")
		}
	}
}

func TestSNMPNestedViewAndUnsupportedUpstreamVariable(t *testing.T) {
	resources := parcelFixtures(t, "snmp-feature")
	data := resources[1].Body["data"].(map[string]any)
	view := data["view"].([]any)[0].(map[string]any)
	oid := view["oid"].([]any)[0].(map[string]any)
	if oid["id"].(map[string]any)["value"] != "1.3.6.1" {
		t.Fatal("nested OID mapping lost")
	}
	if _, _, err := scalarParcelData("snmp", map[string]any{"groups": []any{map[string]any{"name": "g", "security_level": "auth-priv", "view_variable": "view"}}}); err == nil {
		t.Fatal("unusable upstream variable silently accepted")
	}
}

func TestAAAComputedGroupNamesAndDefaults(t *testing.T) {
	_, data, err := scalarParcelData("aaa", map[string]any{"radius_groups": []any{map[string]any{"vpn": float64(3)}, map[string]any{}}, "tacacs_groups": []any{map[string]any{"group_name": "explicit"}}})
	if err != nil {
		t.Fatal(err)
	}
	radius := data["radius"].([]any)
	for i, want := range []string{"radius-0-3", "radius-1-1"} {
		if radius[i].(map[string]any)["groupName"].(map[string]any)["value"] != want {
			t.Fatal("computed name mismatch")
		}
	}
	if data["serverAuthOrder"].(map[string]any)["value"].([]any)[0] != "local" {
		t.Fatal("upstream authentication order default missing")
	}
}

func TestAAAUpstreamLiteralAndVerbatimVariable(t *testing.T) {
	input := map[string]any{"users": []any{map[string]any{"name": "fixture", "public_key_chains": []any{map[string]any{"key_variable": "public_key"}}}}, "accounting_rules": []any{map[string]any{"id": "rule", "groups": []any{"local"}, "method": "exec", "start_stop_variable": "{{accounting_start}}"}}}
	_, data, err := scalarParcelData("aaa", input)
	if err != nil {
		t.Fatal(err)
	}
	user := data["user"].([]any)[0].(map[string]any)
	key := user["pubkeyChain"].([]any)[0].(map[string]any)
	if key["keyType"].(map[string]any)["value"] != "ssh-rsa" {
		t.Fatal("upstream literal lost")
	}
}
