// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func ssidTestRow(name string) map[string]any {
	return map[string]any{"name": name, "auth_type": "OPEN", "wlan_type": "Enterprise"}
}
func ssidTestModel(rows ...map[string]any) []byte {
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"wireless": map[string]any{"ssids": rows}}})
	return raw
}

func TestWirelessSSIDModelMappings(t *testing.T) {
	row := ssidTestRow("CVK SSID")
	row["auth_type"] = "OPEN_SECURED"
	row["ssid_radio_type"] = "Triple Band"
	row["session_timeout"] = 0
	row["client_rate_limit"] = 0
	row["cckm_tsf_tolerance"] = 0
	row["enabled"] = false
	row["auth_servers"] = []any{"192.0.2.2", "192.0.2.1", "192.0.2.2"}
	rows, err := parseWirelessSSIDs(ssidTestModel(row))
	if err != nil {
		t.Fatal(err)
	}
	m := rows[0].metadata
	if m["authType"] != "OPEN-SECURED" || m["ssidRadioType"] != "Triple band operation(2.4GHz, 5GHz and 6GHz)" || m["clientExclusionTimeout"] != int64(180) || m["isEnabled"] != false {
		t.Fatal("mapping/default", m)
	}
	for _, key := range []string{"sessionTimeOut", "clientRateLimit", "cckmTsfTolerance", "isFastLaneEnabled"} {
		if _, ok := m[key]; ok {
			t.Fatal("invented/unset field", key)
		}
	}
	if !reflect.DeepEqual(m["authServers"], []string{"192.0.2.1", "192.0.2.2"}) {
		t.Fatal("set semantics", m["authServers"])
	}
}

func TestWirelessSSIDCredentialSourceIndices(t *testing.T) {
	last := ssidTestRow("Z-last")
	last["auth_type"] = "WPA2_PERSONAL"
	last["passphrase_version"] = 3
	last["multi_psk_settings"] = []any{map[string]any{"priority": 4, "passphrase_type": "HEX", "passphrase_wo_version": 7}, map[string]any{"priority": 0, "passphrase_wo_version": 2}}
	rows, err := parseWirelessSSIDs(ssidTestModel(last, ssidTestRow("A-first")))
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].name != "A-first" || len(rows[1].credentials) != 3 {
		t.Fatal("ordering/credentials", rows)
	}
	want := map[string]int64{"/ssids/0/passphrase": 3, "/ssids/0/multi_psk_settings/0/passphrase_wo": 7, "/ssids/0/multi_psk_settings/1/passphrase_wo": 2}
	for _, c := range rows[1].credentials {
		if want[c.path] != c.version {
			t.Fatal("source index changed", c)
		}
	}
	data, _ := json.Marshal(rows[1].metadata)
	if strings.Contains(string(data), "passphrase_wo") || strings.Contains(string(data), `"passphrase":`) {
		t.Fatal("credential material entered public model")
	}
	m := rows[1].metadata["multiPSKSettings"].([]map[string]any)
	if m[0]["priority"] != int64(4) || m[1]["priority"] != int64(0) {
		t.Fatal("source multi-PSK order changed")
	}
}

func TestWirelessSSIDRejectInvalidModels(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"missing auth":        func(r map[string]any) { delete(r, "auth_type") },
		"missing wlan":        func(r map[string]any) { delete(r, "wlan_type") },
		"long name":           func(r map[string]any) { r["name"] = strings.Repeat("a", 33) },
		"wrong bool":          func(r map[string]any) { r["enabled"] = "true" },
		"unknown field":       func(r map[string]any) { r["enabledd"] = true },
		"null":                func(r map[string]any) { r["nas_options"] = nil },
		"fractional sentinel": func(r map[string]any) { r["session_timeout"] = 0.5 },
		"API radio label":     func(r map[string]any) { r["ssid_radio_type"] = "5GHz only" },
		"bad rate":            func(r map[string]any) { r["client_rate_limit"] = 1 },
		"bad set":             func(r map[string]any) { r["nas_options"] = []any{1} },
		"invalid version":     func(r map[string]any) { r["passphrase_version"] = 0 },
		"duplicate priority": func(r map[string]any) {
			r["multi_psk_settings"] = []any{map[string]any{"priority": 0}, map[string]any{"priority": 0}}
		},
		"bad multi field": func(r map[string]any) { r["multi_psk_settings"] = []any{map[string]any{"priority": 0, "typo": true}} },
		"bad priority":    func(r map[string]any) { r["multi_psk_settings"] = []any{map[string]any{"priority": 5}} },
	} {
		t.Run(name, func(t *testing.T) {
			r := ssidTestRow("CVK")
			change(r)
			if _, err := parseWirelessSSIDs(ssidTestModel(r)); err == nil {
				t.Fatal("invalid intent accepted")
			}
		})
	}
	for _, key := range []string{"passphrase", "passphrase_wo"} {
		for _, nested := range []bool{false, true} {
			r := ssidTestRow("CVK")
			if nested {
				r["multi_psk_settings"] = []any{map[string]any{"priority": 0, key: "secret-sentinel"}}
			} else {
				r[key] = "secret-sentinel"
			}
			_, err := parseWirelessSSIDs(ssidTestModel(r))
			if err == nil || strings.Contains(err.Error(), "secret-sentinel") {
				t.Fatal("inline secret accepted or leaked")
			}
		}
	}
}

func TestWirelessSSIDAllScalarFields(t *testing.T) {
	if len(wirelessSSIDFields) != 63 {
		t.Fatal("pinned scalar coverage changed", len(wirelessSSIDFields))
	}
	r := ssidTestRow("CVK")
	for key, field := range wirelessSSIDFields {
		if key == "name" || key == "auth_type" || key == "wlan_type" {
			continue
		}
		if key == "ssid_radio_type" {
			r[key] = "Triple Band"
			continue
		}
		switch field.kind {
		case "bool":
			r[key] = false
		case "int":
			r[key] = field.min
		case "string":
			r[key] = "test"
			if field.enums != "" {
				r[key] = strings.Split(field.enums, "|")[0]
			}
		}
	}
	if _, err := parseWirelessSSIDs(ssidTestModel(r)); err != nil {
		t.Fatal(err)
	}
}
