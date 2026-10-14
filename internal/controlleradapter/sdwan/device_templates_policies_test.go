// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"os"
	"strings"
	"testing"
)

func policyDeviceTemplateFixture(t *testing.T) []Resource {
	t.Helper()
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-device-template-policies.yaml")
	if err != nil {
		t.Fatal(err)
	}
	resources, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	return resources
}

func TestPolicyDeviceTemplateFixture(t *testing.T) {
	resources := policyDeviceTemplateFixture(t)
	device := resources[len(resources)-1]
	if len(resources) != 6 || device.Kind != "device-template" || len(device.DevicePolicyRefs) != 2 || len(device.DeviceTemplateRefs) != 2 {
		t.Fatal("device policy dependencies missing")
	}
	if device.DevicePolicyRefs[0].Kind != "localized-policy" || device.DevicePolicyRefs[1].Kind != "security-policy" {
		t.Fatal("device policy references misordered")
	}
	raw, _ := os.ReadFile("../../../examples/configs/sdwan/classic-device-template-policies.yaml")
	for _, bad := range []string{
		strings.Replace(string(raw), "CVK-POLICY-DEVICE-LOCALIZED\n      security_policy", "MISSING\n      security_policy", 1),
		strings.Replace(string(raw), "name: CVK-POLICY-DEVICE-SECURITY\n", "name: MISSING\n", 1),
	} {
		if _, err := Compile([]byte(bad)); err == nil {
			t.Fatal("undeclared device policy accepted")
		}
	}
}

func TestLivePolicyDeviceTemplateLifecycle(t *testing.T) {
	liveParcelLifecycle(t, policyDeviceTemplateFixture(t))
}
