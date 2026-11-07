// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRoutingOSPFNestedPayloads(t *testing.T) {
	for _, r := range parcelFixtures(t, "routing-ospf") {
		if r.Kind != "parcel" {
			continue
		}
		data := r.Body["data"].(map[string]any)
		areas := data["area"].([]any)
		row := areas[0].(map[string]any)
		interfaceKey, numberKey := "interfaces", "areaNum"
		if !strings.Contains(r.Type, "ospfv3") {
			interfaceKey, numberKey = "interface", "aNum"
		}
		if len(row[interfaceKey].([]any)) != 1 {
			t.Fatal("interface lost", r.Type)
		}
		if got := row[numberKey].(map[string]any)["value"]; got != float64(0) {
			t.Fatal(got)
		}
	}
}
func TestRoutingOSPFConditionalLSA(t *testing.T) {
	for _, kind := range []string{"service_routing_ospf", "transport_routing_ospf"} {
		for _, setting := range []string{"", "on-startup"} {
			input := map[string]any{"name": "test", "router_lsa_advertisement_type": setting, "router_lsa_advertisement_time": float64(60)}
			_, data, err := scalarParcelData(kind, input)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(data)
			if setting == "" && strings.Contains(string(raw), `"value":60`) {
				t.Fatal("disabled singleton emitted", data)
			}
			if setting != "" && !strings.Contains(string(raw), `"value":60`) {
				t.Fatal("enabled singleton missing", data)
			}
		}
	}
}
func TestRoutingOSPFRejectsInvalidInputs(t *testing.T) {
	for _, input := range []map[string]any{
		{"router_id": "not-an-ip"},
		{"areas": []any{map[string]any{"number": float64(-1)}}},
		{"router_lsa_advertisement_type": true},
		{"unsupported": true},
	} {
		input["name"] = "bad"
		if _, _, err := scalarParcelData("service_routing_ospf", input); err == nil {
			t.Fatal("invalid OSPF accepted", input)
		}
	}
	raw := []byte("sdwan:\n  feature_profiles:\n    service_profiles:\n      - name: test\n        ospf_features:\n          - name: ospf\n            route_policy: missing\n")
	if _, err := Compile(raw); err == nil {
		t.Fatal("undeclared route policy accepted")
	}
	for _, family := range []string{"system", "sse"} {
		if _, err := simpleParcels(FeatureProfile{Name: "bad", OSPF: []map[string]any{{"name": "ospf"}}}, family, "key", ""); err == nil {
			t.Fatal("wrong family accepted")
		}
	}
}
