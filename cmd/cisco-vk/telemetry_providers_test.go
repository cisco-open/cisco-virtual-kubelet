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
