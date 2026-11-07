// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"reflect"
	"testing"
)

func TestClassicSecurityEncoding(t *testing.T) {
	r := parcelFixtures(t, "classic-security")[0]
	data := r.Body["templateDefinition"].(map[string]any)
	if got := classicInput(data, "ipsec.authentication-type.vipValue"); !reflect.DeepEqual(got, []any{"ah-sha1-hmac", "sha1-hmac"}) {
		t.Fatal("authentication translation", got)
	}
	if got := classicInput(data, "ipsec.integrity-type.vipValue"); !reflect.DeepEqual(got, []any{"esp", "ip-udp-esp"}) {
		t.Fatal("integrity source", got)
	}
	if got := classicInput(data, "ipsec.replay-window.vipValue"); got != "512" {
		t.Fatal("numeric replay window", got)
	}
	for _, flag := range []any{nil, "infinite", "duration"} {
		key := map[string]any{"id": float64(1), "key_chain_name": "CHAIN", "crypto_algorithm": "hmac-sha-256", "accept_lifetime_end_time_format": flag, "send_lifetime_start_time_epoch": float64(1700000000)}
		rs, err := compileClassicTemplates("security_templates", []map[string]any{{"name": "test", "description": "test", "keys": []any{key}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		body := rs[0].Body["templateDefinition"].(map[string]any)
		rows := classicInput(body, "key.vipValue").([]any)
		row := rows[0].(map[string]any)
		if got := classicInput(row, "send-lifetime.lifetime-group-v1.start-epoch.vipValue"); got != "1700000000" {
			t.Fatal(got)
		}
		got := classicInput(row, "accept-lifetime.lifetime-group-v1.infinite.vipValue")
		if flag == "infinite" && got == nil {
			t.Fatal("missing infinite flag")
		}
		if flag != "infinite" && got != nil {
			t.Fatal("unexpected infinite flag")
		}
	}
	for _, input := range []map[string]any{
		{"authentication_types": []any{"invalid"}}, {"authentication_types": "esp"}, {"replay_window": float64(513)},
	} {
		input["name"] = "bad"
		input["description"] = "bad"
		if _, err := compileClassicTemplates("security_templates", []map[string]any{input}, nil); err == nil {
			t.Fatal("invalid security accepted", input)
		}
	}
}

func TestClassicOSPFAreaConditions(t *testing.T) {
	for _, kind := range []string{"stub", "nssa", "normal"} {
		for _, summary := range []any{nil, true, false} {
			input := map[string]any{"name": "test", "description": "test", "areas": []any{map[string]any{"area_number": float64(1), "area_type": kind, "no_summary": summary}}, "route_policy_variable": "policy"}
			rs, err := compileClassicTemplates("ospf_templates", []map[string]any{input}, nil)
			if err != nil {
				t.Fatal(err)
			}
			data := rs[0].Body["templateDefinition"].(map[string]any)
			row := classicInput(data, "ospf.area.vipValue").([]any)[0].(map[string]any)
			got := classicInput(row, kind+".no-summary.vipValue")
			var want any
			if kind != "normal" {
				if summary == nil {
					want = "false"
				} else if summary == true {
					want = "true"
				}
			}
			if got != want {
				t.Fatalf("%s/%v got %v want %v", kind, summary, got, want)
			}
			policy := classicInput(data, "ospf.route-policy.vipValue").([]any)[0].(map[string]any)
			if classicInput(policy, "pol-name.vipVariableName") != "policy" || classicInput(policy, "direction.vipValue") != "in" {
				t.Fatal(policy)
			}
		}
	}
	for _, flag := range []any{false, true} {
		rs, err := compileClassicTemplates("ospf_templates", []map[string]any{{"name": "test", "description": "test", "default_information_originate": flag, "default_information_originate_metric": float64(10)}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		data := rs[0].Body["templateDefinition"].(map[string]any)
		if classicInput(data, "ospf.default-information.originate.metric.vipValue") != float64(10) {
			t.Fatal("container erased child")
		}
	}
}

func TestClassicSIGDerivedDescription(t *testing.T) {
	for name, want := range map[string]string{"Cisco-Umbrella-Global-Credentials": "Global credentials for umbrella", "other": "Global credentials for zscaler"} {
		rs, err := compileClassicTemplates("sig_credentials_templates", []map[string]any{{"name": name}}, nil)
		if err != nil || rs[0].Body["templateDescription"] != want {
			t.Fatal(rs, err)
		}
	}
}

func TestClassicSIGGlobalAndVariablePayload(t *testing.T) {
	r := parcelFixtures(t, "classic-sig-credentials")[0]
	if r.Body["isGlobal"] != true {
		t.Fatal("global credential flag missing")
	}
	data := r.Body["templateDefinition"].(map[string]any)
	for _, path := range []string{"umbrella.api-key", "umbrella.api-secret"} {
		leaf := classicInput(data, path).(map[string]any)
		if value, exists := leaf["vipValue"]; !exists || value != "" {
			t.Fatal("variable secret requires an empty vipValue", leaf)
		}
	}
	changed := map[string]any{}
	for k, v := range r.Body {
		changed[k] = v
	}
	changed["isGlobal"] = false
	if same, err := Equal(r, changed); err != nil || same {
		t.Fatal("global flag drift ignored", err)
	}
	for _, input := range []map[string]any{
		{"zscaler_organization_variable": "org"},
		{"zscaler_organization": "example"},
	} {
		input["name"] = "test"
		rs, err := compileClassicTemplates("sig_credentials_templates", []map[string]any{input}, nil)
		if err != nil {
			t.Fatal(err)
		}
		leaf := classicInput(rs[0].Body["templateDefinition"].(map[string]any), "zscaler.organization").(map[string]any)
		if _, ok := leaf["vipValue"]; !ok {
			t.Fatal("missing value")
		}
		if _, ok := leaf["vipVariableName"]; !ok {
			t.Fatal("missing variable name")
		}
	}
}
