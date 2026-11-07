// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"os"
	"testing"
)

func vpnDeviceTemplateFixture(t *testing.T) []Resource {
	t.Helper()
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-device-template-vpns.yaml")
	if err != nil {
		t.Fatal(err)
	}
	resources, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	return resources
}

func TestVPNDeviceTemplateFixture(t *testing.T) {
	resources := vpnDeviceTemplateFixture(t)
	device := resources[len(resources)-1]
	if len(resources) != 11 || device.Kind != "device-template" || len(device.DeviceTemplateRefs) != 10 {
		t.Fatal("nested VPN dependencies missing")
	}
	general := device.Body["generalTemplates"].([]any)
	if len(general) != 4 {
		t.Fatal("system and three VPN references missing")
	}
}

func TestLiveVPNDeviceTemplateLifecycle(t *testing.T) {
	liveParcelLifecycle(t, vpnDeviceTemplateFixture(t))
}
