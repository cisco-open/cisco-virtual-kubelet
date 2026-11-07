// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"os"
	"testing"
)

func classicAAAFixture(t *testing.T) []Resource {
	t.Helper()
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-aaa.yaml")
	if err != nil {
		t.Fatal(err)
	}
	resources, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	return resources
}

func TestClassicAAAFixture(t *testing.T) {
	resources := classicAAAFixture(t)
	if len(resources) != 1 || resources[0].Type != "cedge_aaa" {
		t.Fatal("invalid AAA fixture")
	}
}

func TestClassicAAANestedModel(t *testing.T) {
	raw := []byte(`sdwan:
  edge_feature_templates:
    aaa_templates:
      - name: CVK-AAA-NESTED
        description: nested local model check
        device_types: [C8000V]
        accounting_rules:
          - method: exec
            groups: [local]
        radius_dynamic_author:
          clients:
            - ip: 192.0.2.10
              vpn_id: 10
              server_key: fixture-key
`)
	resources, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	definition := resources[0].Body["templateDefinition"].(map[string]any)
	order := definition["server-auth-order"].(map[string]any)
	if order["vipValue"] != "local" {
		t.Fatal("default authentication order was not joined")
	}
	accounting := definition["accounting"].(map[string]any)["accounting-rule"].(map[string]any)["vipValue"].([]any)
	if accounting[0].(map[string]any)["rule-id"].(map[string]any)["vipValue"] != "0" {
		t.Fatal("accounting rule index was not generated")
	}
	clients := definition["radius-dynamic-author"].(map[string]any)["radius-client"].(map[string]any)["vipValue"].([]any)
	vpn := clients[0].(map[string]any)["vpn"].(map[string]any)["vipValue"].([]any)
	if vpn[0].(map[string]any)["name"].(map[string]any)["vipValue"] != float64(10) {
		t.Fatal("nested RADIUS client VPN was not compiled")
	}
}

func TestLiveClassicAAALifecycle(t *testing.T) {
	liveParcelLifecycle(t, classicAAAFixture(t))
}
