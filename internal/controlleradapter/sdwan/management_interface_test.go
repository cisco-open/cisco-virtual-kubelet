// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"os"
	"strings"
	"testing"
)

func TestManagementInterfaceParentAndVersion(t *testing.T) {
	resources := parcelFixtures(t, "transport-management-interface")
	if len(resources) != 3 || resources[2].ParentParcelName != resources[1].Name || resources[2].ParentParcelPath != "management/vpn" {
		t.Fatal("management Ethernet must follow its declared VPN parent")
	}
	child := resources[2]
	data := child.Body["data"].(map[string]any)
	if _, ok := data["intfIpAddress"].(map[string]any)["either"]; !ok {
		t.Fatal("20.18 address payload must use either branch")
	}
	withoutParent := append([]Resource{}, resources...)
	withoutParent = append(withoutParent[:1], withoutParent[2:]...)
	if _, err := orderParcelDependencies(withoutParent); err == nil {
		t.Fatal("nested parcel without parent was accepted")
	}
	raw, err := os.ReadFile("../../../examples/configs/sdwan/transport-management-interface.yaml")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := Compile([]byte(strings.Replace(string(raw), `manager_version: "20.18"`, `manager_version: "20.15"`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	legacyData := legacy[2].Body["data"].(map[string]any)
	if _, ok := legacyData["intfIpAddress"].(map[string]any)["static"]; !ok {
		t.Fatal("pre-20.18 address payload must use legacy static branch")
	}
}

func TestManagementInterfaceRejectsUnsupportedAddressCombination(t *testing.T) {
	base := map[string]any{"name": "eth", "interface_name": "GigabitEthernet1", "ipv4_address_type": "dynamic", "ipv4_address": "192.0.2.10"}
	if _, err := managementInterfaceData(base, true); err == nil {
		t.Fatal("static address with dynamic addressing was accepted")
	}
	base["ipv4_address_type"] = "static"
	base["ipv4_address_type_variable"] = "address_type"
	if _, err := managementInterfaceData(base, true); err == nil {
		t.Fatal("address type and variable conflict was accepted")
	}
}
