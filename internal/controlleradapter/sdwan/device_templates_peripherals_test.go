// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"os"
	"testing"
)

func peripheralDeviceTemplateFixture(t *testing.T) []Resource {
	t.Helper()
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-device-template-peripherals.yaml")
	if err != nil {
		t.Fatal(err)
	}
	resources, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	return resources
}

func TestPeripheralDeviceTemplateFixture(t *testing.T) {
	resources := peripheralDeviceTemplateFixture(t)
	device := resources[len(resources)-1]
	if len(resources) != 5 || device.Kind != "device-template" || len(device.DeviceTemplateRefs) != 4 {
		t.Fatal("peripheral device template dependencies missing")
	}
	general := device.Body["generalTemplates"].([]any)
	if len(general) != 3 || len(general[2].(map[string]any)["subTemplates"].([]any)) != 1 {
		t.Fatal("cellular profile nesting missing")
	}
}

func TestLivePeripheralDeviceTemplateLifecycle(t *testing.T) {
	liveParcelLifecycle(t, peripheralDeviceTemplateFixture(t))
}
