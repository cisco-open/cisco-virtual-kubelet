// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"os"
	"testing"
)

func classicEthernetInterfaceFixture(t *testing.T) []Resource {
	t.Helper()
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-ethernet-interface.yaml")
	if err != nil {
		t.Fatal(err)
	}
	resources, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	return resources
}

func TestClassicEthernetInterfaceFixture(t *testing.T) {
	resources := classicEthernetInterfaceFixture(t)
	if len(resources) != 1 || resources[0].Type != "cisco_vpn_interface" {
		t.Fatal("invalid Ethernet interface fixture")
	}
}

func TestLiveClassicEthernetInterfaceLifecycle(t *testing.T) {
	liveParcelLifecycle(t, classicEthernetInterfaceFixture(t))
}
