// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import "testing"

func TestServiceMulticastNestedRows(t *testing.T) {
	resources := parcelFixtures(t, "service-multicast")
	var data map[string]any
	for _, r := range resources {
		if r.Type == "service_multicast" {
			data = r.Body["data"].(map[string]any)
		}
	}
	if data == nil {
		t.Fatal("multicast parcel missing")
	}
	igmp := classicInput(data, "igmp.interface").([]any)
	join := classicInput(igmp[0].(map[string]any), "joinGroup").([]any)
	if len(join) != 1 || classicInput(join[0].(map[string]any), "groupAddress.value") != "239.1.1.1" {
		t.Fatal("IGMP join group mapping changed", join)
	}
	if classicInput(data, "pim.ssm.ssmRangeConfig.enableSSMFlag.value") != true {
		t.Fatal("PIM source-specific multicast flag missing")
	}
	if _, _, err := scalarParcelData("service_multicast", map[string]any{"name": "bad"}); err == nil {
		t.Fatal("missing PIM source-specific multicast flag accepted")
	}
}
