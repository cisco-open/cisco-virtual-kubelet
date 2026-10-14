// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"os"
	"testing"
)

func classicBGPFixture(t *testing.T) []Resource {
	t.Helper()
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-bgp.yaml")
	if err != nil {
		t.Fatal(err)
	}
	resources, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	return resources
}

func TestClassicBGPFixture(t *testing.T) {
	resources := classicBGPFixture(t)
	if len(resources) != 1 || resources[0].Type != "cisco_bgp" {
		t.Fatal("invalid BGP fixture")
	}
}

func TestClassicBGPNestedNeighborPolicy(t *testing.T) {
	_, err := Compile([]byte(`sdwan:
  edge_feature_templates:
    bgp_templates:
      - name: CVK-BGP-NESTED
        description: isolated local BGP model
        device_types: [C8000V]
        as_number: 65000
        ipv4_address_family:
          neighbors:
            - address: 192.0.2.2
              remote_as: "65001"
              address_families:
                - family_type: ipv4-unicast
                  route_policy_in: CVK-ROUTE-POLICY
`))
	if err != nil {
		t.Fatal(err)
	}
}

func TestLiveClassicBGPLifecycle(t *testing.T) {
	liveParcelLifecycle(t, classicBGPFixture(t))
}
