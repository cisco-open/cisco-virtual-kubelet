// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"os"
	"testing"
)

func classicSIGFixture(t *testing.T) []Resource {
	t.Helper()
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-sig.yaml")
	if err != nil {
		t.Fatal(err)
	}
	resources, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	return resources
}

func TestClassicSIGFixture(t *testing.T) {
	resources := classicSIGFixture(t)
	if len(resources) != 1 || resources[0].Type != "cisco_secure_internet_gateway" {
		t.Fatal("invalid SIG fixture")
	}
	data := resources[0].Body["templateDefinition"].(map[string]any)
	if _, ok := data["service"]; !ok {
		t.Fatal("SIG service row missing")
	}
}

func TestClassicSIGSelectors(t *testing.T) {
	defaults := map[string]any{"ike_group": float64(14), "ipsec_rekey_interval": float64(3600)}
	input := map[string]any{"sig_provider": "umbrella", "interfaces": []any{map[string]any{"tunnel_type": "ipsec", "name": "Tunnel1", "ike_pre_shared_key": "fixture", "tunnel_destination": "192.0.2.1"}}}
	prepared, err := prepareSIGTemplate(input, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := prepared["sig_provider"]; exists {
		t.Fatal("root provider selector leaked into provider model")
	}
	row := prepared["interfaces"].([]any)[0].(map[string]any)
	if row["auto_tunnel_mode"] != true || row["ike_pre_shared_key_dynamic"] != true || row["ike_version"] != float64(2) || row["ike_group"] != float64(14) || row["tunnel_destination"] != "dynamic" || row["sig_provider"] != "secure-internet-gateway-umbrella" {
		t.Fatal("SIG provider and IPsec selectors were not applied")
	}
	input = map[string]any{"sig_provider": "other", "interfaces": []any{map[string]any{"tunnel_type": "gre", "name": "Tunnel2", "ike_group": float64(14), "tunnel_destination": "192.0.2.2"}}}
	prepared, err = prepareSIGTemplate(input, defaults)
	if err != nil {
		t.Fatal(err)
	}
	row = prepared["interfaces"].([]any)[0].(map[string]any)
	if row["auto_tunnel_mode"] != false || row["ike_group"] != nil || row["tunnel_destination"] != "192.0.2.2" {
		t.Fatal("SIG GRE selectors were not applied")
	}
	_, err = Compile([]byte(`sdwan:
  edge_feature_templates:
    secure_internet_gateway_templates:
      - name: CVK-SIG-HA
        description: isolated local HA model
        device_types: [C8000V]
        sig_provider: other
        high_availability_interface_pairs:
          - active_interface: Tunnel1
            backup_interface: none
`))
	if err != nil {
		t.Fatal("SIG HA pair mapping:", err)
	}
}

func TestLiveClassicSIGLifecycle(t *testing.T) {
	liveParcelLifecycle(t, classicSIGFixture(t))
}
