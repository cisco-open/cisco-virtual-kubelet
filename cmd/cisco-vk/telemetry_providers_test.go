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

package main

import "testing"

func TestNormalizeOTLPEndpoint(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		insecure bool
		want     string
	}{
		{"bare host:port insecure adds http", "192.168.129.23:4317", true, "http://192.168.129.23:4317"},
		{"bare host:port secure adds https", "otelcol.observability:4317", false, "https://otelcol.observability:4317"},
		{"already-http preserved", "http://otelcol:4317", true, "http://otelcol:4317"},
		{"already-https preserved", "https://otelcol:4317", false, "https://otelcol:4317"},
		{"mixed-case scheme preserved", "HTTPS://otelcol:4317", false, "HTTPS://otelcol:4317"},
		{"empty stays empty", "", true, ""},
		{"trims whitespace", "  192.168.129.23:4317  ", true, "http://192.168.129.23:4317"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeOTLPEndpoint(tc.input, tc.insecure)
			if got != tc.want {
				t.Errorf("normalizeOTLPEndpoint(%q, %v) = %q; want %q", tc.input, tc.insecure, got, tc.want)
			}
		})
	}
}

func TestSerializedStringMapParsesJSON(t *testing.T) {
	got, err := serializedStringMap(`{"authorization":"Bearer token","x-scope-orgid":"network"}`)
	if err != nil {
		t.Fatalf("serializedStringMap returned error: %v", err)
	}
	want := map[string]string{
		"authorization": "Bearer token",
		"x-scope-orgid": "network",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s=%q, want %q", k, got[k], v)
		}
	}
}

func TestSerializedStringMapParsesCommaSeparatedHeaders(t *testing.T) {
	got, err := serializedStringMap("authorization=Bearer token,x-scope-orgid=network")
	if err != nil {
		t.Fatalf("serializedStringMap returned error: %v", err)
	}
	if got["authorization"] != "Bearer token" {
		t.Errorf("authorization=%q, want Bearer token", got["authorization"])
	}
	if got["x-scope-orgid"] != "network" {
		t.Errorf("x-scope-orgid=%q, want network", got["x-scope-orgid"])
	}
}

func TestSerializedStringMapRejectsMalformedEntry(t *testing.T) {
	if _, err := serializedStringMap("authorization"); err == nil {
		t.Fatal("expected malformed entry to fail")
	}
}

func TestTelemetryResourceAttributesMergesEnv(t *testing.T) {
	t.Setenv(envCVKResourceAttributes, `{"deployment.environment":"lab","site.id":"sjc01"}`)

	got, err := telemetryResourceAttributes(map[string]string{
		"service.name":      "cisco-vk-telemetry",
		"cisco.device.name": "c9300x-01",
	})
	if err != nil {
		t.Fatalf("telemetryResourceAttributes returned error: %v", err)
	}
	want := map[string]string{
		"service.name":           "cisco-vk-telemetry",
		"cisco.device.name":      "c9300x-01",
		"deployment.environment": "lab",
		"site.id":                "sjc01",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s=%q, want %q", k, got[k], v)
		}
	}
}
