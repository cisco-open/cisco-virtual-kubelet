// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package nacmodel

import (
	"encoding/json"
	"strings"
	"testing"
)

func defaultsModel(category, profile string) []byte {
	return []byte(`{"catalyst_center":{"sites":{"global":{"network_settings":{"` + category + `":"base"}}},"network_settings":{"` + category + `":[{"name":"base",` + profile + `}]}}}`)
}
func exportedProfile(t *testing.T, raw, defaults []byte, category string) map[string]any {
	t.Helper()
	out, meta, err := ExportNetworkDefaults(raw, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ConfigurationParity || meta.DefaultsSHA256 != DefaultsSHA256 || meta.UpstreamRevision != Revision || meta.OutputSHA256 != defaultsDigest(out) {
		t.Fatal("export provenance overclaims or lost pin")
	}
	var model map[string]any
	if json.Unmarshal(out, &model) != nil {
		t.Fatal("invalid exported JSON")
	}
	return model[Stripe].(map[string]any)["network_settings"].(map[string]any)[category].([]any)[0].(map[string]any)
}

func TestNetworkDefaultsExpressions(t *testing.T) {
	custom := []byte(`defaults:
  catalyst_center:
    network_settings:
      telemetry:
        wired_data_collection: false
        snmp_servers: []
      network:
        ntp_servers: [192.0.2.1]
        dns_servers: [192.0.2.53]
`)
	t.Run("pinned-and-false", func(t *testing.T) {
		p := exportedProfile(t, defaultsModel("telemetry", `"wired_data_collection":false`), nil, "telemetry")
		if p["wired_data_collection"] != false || p["wireless_telemetry"] != false || p["catalyst_center_as_network_collector"] != true || p["catalyst_center_as_syslog_server"] != true || p["enable_netflow_collector_on_devices"] != false {
			t.Fatal("wrong pinned defaults/explicit false")
		}
		if _, ok := p["snmp_servers"]; ok {
			t.Fatal("invented list default")
		}
	})
	t.Run("null-is-not-missing", func(t *testing.T) {
		p := exportedProfile(t, defaultsModel("telemetry", `"wired_data_collection":null`), custom, "telemetry")
		if p["wired_data_collection"] != nil || len(p["snmp_servers"].([]any)) != 0 {
			t.Fatal("try/null semantics changed")
		}
	})
	t.Run("override", func(t *testing.T) {
		p := exportedProfile(t, defaultsModel("telemetry", `"wireless_telemetry":true`), custom, "telemetry")
		if p["wired_data_collection"] != false || p["wireless_telemetry"] != true {
			t.Fatal("override precedence wrong")
		}
	})
	t.Run("selection-before-defaults", func(t *testing.T) {
		p := exportedProfile(t, defaultsModel("network", `"banner":"test"`), custom, "network")
		if _, ok := p["ntp_servers"]; ok {
			t.Fatal("defaults activated unselected resource")
		}
		if _, ok := p["dns_servers"]; ok {
			t.Fatal("unselected DNS acquired server default")
		}
	})
	t.Run("selected-dns", func(t *testing.T) {
		p := exportedProfile(t, defaultsModel("network", `"domain_name":"example.test"`), custom, "network")
		if len(p["dns_servers"].([]any)) != 1 {
			t.Fatal("DNS optional fallback missing")
		}
	})
	t.Run("dns-null", func(t *testing.T) {
		p := exportedProfile(t, defaultsModel("network", `"domain_name":"example.test","dns_servers":null`), custom, "network")
		if p["dns_servers"] != nil {
			t.Fatal("explicit null DNS fell back")
		}
	})
	t.Run("builtin-conditional", func(t *testing.T) {
		p := exportedProfile(t, defaultsModel("telemetry", `"netflow_collector_ip_address":"192.0.2.1","netflow_collector_port":2055`), nil, "telemetry")
		if _, ok := p["netflow_collector_ip_address"]; ok {
			t.Fatal("Builtin conditional retained suppressed address")
		}
	})
	t.Run("external-fallback", func(t *testing.T) {
		override := []byte(`defaults: {catalyst_center: {network_settings: {telemetry: {netflow_collector_ip_address: 192.0.2.1, netflow_collector_port: 2055}}}}`)
		p := exportedProfile(t, defaultsModel("telemetry", `"catalyst_center_as_network_collector":false`), override, "telemetry")
		if p["netflow_collector_ip_address"] != "192.0.2.1" || p["netflow_collector_port"] != float64(2055) {
			t.Fatal("external fallback wrong")
		}
	})
}

func TestNetworkDefaultsRefusesAmbiguity(t *testing.T) {
	valid := defaultsModel("telemetry", `"wired_data_collection":true`)
	for _, tc := range []struct {
		name            string
		model, defaults []byte
	}{
		{"null-collector", defaultsModel("telemetry", `"catalyst_center_as_network_collector":null`), nil},
		{"unresolved-external", defaultsModel("telemetry", `"catalyst_center_as_network_collector":false`), nil},
		{"other-section", []byte(`{"catalyst_center":{"inventory":{"devices":[]}}}`), nil},
		{"credentials", []byte(`{"catalyst_center":{"network_settings":{"device_credentials":{}}}}`), nil},
		{"duplicate", []byte(`{"catalyst_center":{},"catalyst_center":{}}`), nil},
		{"null-default", valid, []byte(`defaults: {catalyst_center: {network_settings: {telemetry: {wired_data_collection: null}}}}`)},
		{"wrong-default-category", valid, []byte(`defaults: {catalyst_center: {network_settings: {network: {wired_data_collection: true}}}}`)},
		{"other-default-section", valid, []byte(`defaults: {catalyst_center: {system_settings: {password: NEVER_PRINT_THIS}}}`)},
		{"duplicate-default", valid, []byte(`defaults: {catalyst_center: {network_settings: {telemetry: {wired_data_collection: true, wired_data_collection: false}}}}`)},
		{"alias-default", valid, []byte(`defaults: &defaults {catalyst_center: {network_settings: {telemetry: {}}}}`)},
		{"multi-doc-default", valid, []byte("defaults: {}\n---\ndefaults: {}")},
		{"too-large", []byte(strings.Repeat("x", (1<<20)+1)), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, meta, err := ExportNetworkDefaults(tc.model, tc.defaults)
			if err == nil || len(out) != 0 || meta.Format != "" || strings.Contains(err.Error(), "NEVER_PRINT_THIS") {
				t.Fatal("ambiguous export or input disclosure")
			}
		})
	}
	if defaultsDigest(pinnedDefaults) != DefaultsSHA256 {
		t.Fatal("pinned source changed")
	}
}
