// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const telemetryProfile = `{"name":"base","wired_data_collection":false,"wireless_telemetry":true,"catalyst_center_as_snmp_server":true,"snmp_servers":[],"catalyst_center_as_syslog_server":false,"syslog_servers":["192.0.2.2","192.0.2.1"],"catalyst_center_as_network_collector":false,"enable_netflow_collector_on_devices":true,"netflow_collector_ip_address":"192.0.2.90","netflow_collector_port":2055}`
const telemetryResponse = `{"wiredDataCollection":{"enableWiredDataCollection":false},"wirelessTelemetry":{"enableWirelessTelemetry":true},"snmpTraps":{"useBuiltinTrapServer":true,"externalTrapServers":[]},"syslogs":{"useBuiltinSyslogServer":false,"externalSyslogServers":["192.0.2.1","192.0.2.2"]},"applicationVisibility":{"enableOnWiredAccessDevices":true,"collector":{"collectorType":"TelemetryBrokerOrUDPDirector","address":"192.0.2.90","port":2055}}}`

func telemetryModel(profile string) []byte {
	return []byte(`{"catalyst_center":{"sites":{"global":{"network_settings":{"telemetry":"base"}}},"network_settings":{"telemetry":[` + profile + `]}}}`)
}

func TestTelemetryReport(t *testing.T) {
	for _, mode := range []string{"match", "changed", "inherited", "unset", "missing-bool", "null-bool", "missing-list", "bad-collector", "missing-group", "partial"} {
		t.Run(mode, func(t *testing.T) {
			bindings, err := parseNetworkSettings(telemetryModel(telemetryProfile))
			if err != nil {
				t.Fatal(err)
			}
			body := telemetryResponse
			switch mode {
			case "changed":
				body = strings.Replace(body, `"enableWiredDataCollection":false`, `"enableWiredDataCollection":true`, 1)
			case "inherited":
				body = strings.Replace(body, `{"enableWiredDataCollection":false}`, `null`, 1)
			case "unset":
				body = strings.Replace(body, `{"enableWiredDataCollection":false}`, `{}`, 1)
			case "missing-bool":
				body = strings.Replace(body, `"enableWiredDataCollection":false`, `"unexpected":false`, 1)
			case "null-bool":
				body = strings.Replace(body, `"enableWiredDataCollection":false`, `"enableWiredDataCollection":null`, 1)
			case "missing-list":
				body = strings.Replace(body, `,"externalTrapServers":[]`, ``, 1)
			case "bad-collector":
				body = strings.Replace(body, `"port":2055`, `"port":0`, 1)
			case "missing-group":
				body = strings.Replace(body, `"wiredDataCollection"`, `"unknown"`, 1)
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected mutation")
				}
				if r.URL.Path == sitesPath {
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
					return
				}
				if !strings.HasSuffix(r.URL.Path, "/telemetrySettings") || r.URL.Query().Get("_inherited") != "false" {
					t.Error("wrong API")
				}
				if mode == "partial" {
					w.WriteHeader(206)
				}
				fmt.Fprint(w, `{"response":`+body+`}`)
			})
			drift, err := c.reportNetworkSettings(context.Background(), bindings)
			valid := mode == "match" || mode == "changed" || mode == "inherited" || mode == "unset"
			if (err == nil) != valid {
				t.Fatalf("valid=%v err=%v", valid, err)
			}
			if !valid {
				if len(drift) != 0 {
					t.Fatal("partial drift escaped")
				}
				return
			}
			want := 1
			if mode == "match" {
				want = 0
			}
			if len(drift) != want {
				t.Fatalf("drift=%d", len(drift))
			}
			status, _ := json.Marshal(drift)
			if strings.Contains(string(status), "192.0.2") {
				t.Fatal("address leaked")
			}
		})
	}
}
func TestTelemetryIntentValidation(t *testing.T) {
	for _, profile := range []string{
		strings.Replace(telemetryProfile, `"netflow_collector_port":2055`, `"netflow_collector_port":65536`, 1),
		strings.Replace(telemetryProfile, `"netflow_collector_port":2055`, `"netflow_collector_port":2.5`, 1),
		strings.Replace(telemetryProfile, `"catalyst_center_as_network_collector":false`, `"catalyst_center_as_network_collector":true`, 1),
		strings.Replace(telemetryProfile, `"name":"base"`, `"name":"base","unknown":true`, 1),
	} {
		if _, err := parseNetworkSettings(telemetryModel(profile)); err == nil {
			t.Fatal("invalid telemetry intent accepted")
		}
	}
	builtin := strings.Replace(telemetryProfile, `"catalyst_center_as_network_collector":false`, `"catalyst_center_as_network_collector":true`, 1)
	builtin = strings.Replace(builtin, `,"netflow_collector_ip_address":"192.0.2.90","netflow_collector_port":2055`, ``, 1)
	if _, err := parseNetworkSettings(telemetryModel(builtin)); err != nil {
		t.Fatal(err)
	}
	// Profile names are scoped by category; both profiles may share a name.
	combined := strings.Replace(string(telemetryModel(builtin)), `"telemetry":"base"`, `"telemetry":"base","network":"base"`, 1)
	combined = strings.Replace(combined, `"telemetry":[`, `"network":[{"name":"base","timezone":"GMT"}],"telemetry":[`, 1)
	bindings, err := parseNetworkSettings([]byte(combined))
	if err != nil || len(bindings[0].settings) != 2 {
		t.Fatalf("combined profiles: %v", err)
	}
}
