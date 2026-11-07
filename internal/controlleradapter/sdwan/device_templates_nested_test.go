// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"os"
	"testing"
)

func nestedDeviceTemplateFixture(t *testing.T) []Resource {
	t.Helper()
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-device-template-nested.yaml")
	if err != nil {
		t.Fatal(err)
	}
	resources, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	return resources
}

func TestNestedDeviceTemplateFixture(t *testing.T) {
	resources := nestedDeviceTemplateFixture(t)
	if len(resources) != 6 || resources[len(resources)-1].Kind != "device-template" || len(resources[len(resources)-1].DeviceTemplateRefs) != 5 {
		t.Fatal("nested device template dependencies missing")
	}
	general := resources[len(resources)-1].Body["generalTemplates"].([]any)
	if len(general) != 3 || len(general[2].(map[string]any)["subTemplates"].([]any)) != 2 {
		t.Fatal("nested VPN references missing")
	}
}

func TestLiveNestedDeviceTemplateLifecycle(t *testing.T) {
	liveParcelLifecycle(t, nestedDeviceTemplateFixture(t))
}
