// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"os"
	"testing"
)

func TestClassicVPNDerivedRouteSubtypes(t *testing.T) {
	raw := []byte(`sdwan:
  edge_feature_templates:
    vpn_templates:
      - name: CVK-ROUTE-SUBTYPE
        description: CVK derived route subtype fixture
        device_types: [C8000V]
        vpn_id: 24
        omp_advertise_ipv4_routes:
          - protocol: ospf
        route_global_exports:
          - protocol: ospf
        route_global_imports:
          - protocol: static
`)
	resources, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	data := resources[0].Body["templateDefinition"].(map[string]any)
	for _, path := range []string{"omp.advertise", "route-export", "route-import"} {
		tree, ok := classicInput(data, path).(map[string]any)
		if !ok {
			t.Fatal("missing route tree", path)
		}
		row := tree["vipValue"].([]any)[0].(map[string]any)
		subtype := row["protocol-sub-type"].(map[string]any)
		if subtype["vipType"] != "constant" {
			t.Fatal("missing external route subtype", path)
		}
	}
}

func TestLiveClassicVPNRouteSubtypesLifecycle(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-vpn-routes.yaml")
	if err != nil { t.Fatal(err) }
	resources, err := Compile(raw)
	if err != nil { t.Fatal(err) }
	liveParcelLifecycle(t, resources)
}
