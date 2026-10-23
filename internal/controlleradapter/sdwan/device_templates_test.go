// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"os"
	"strings"
	"testing"
)

func classicDeviceTemplateFixture(t *testing.T) []Resource {
	t.Helper()
	raw, e := os.ReadFile("../../../examples/configs/sdwan/classic-device-template.yaml")
	if e != nil {
		t.Fatal(e)
	}
	rs, e := Compile(raw)
	if e != nil {
		t.Fatal(e)
	}
	return rs
}
func TestClassicDeviceTemplateFixture(t *testing.T) {
	rs := classicDeviceTemplateFixture(t)
	if len(rs) != 3 || rs[2].Kind != "device-template" || len(rs[2].DeviceTemplateRefs) != 2 {
		t.Fatal("feature dependencies missing")
	}
	raw, e := os.ReadFile("../../../examples/configs/sdwan/classic-device-template.yaml")
	if e != nil {
		t.Fatal(e)
	}
	bad := strings.Replace(string(raw), "banner_template: CVK-DEVICE-BANNER", "banner_template: MISSING", 1)
	if _, e = Compile([]byte(bad)); e == nil {
		t.Fatal("undeclared feature template accepted")
	}
}
func TestLiveClassicDeviceTemplateLifecycle(t *testing.T) {
	liveParcelLifecycle(t, classicDeviceTemplateFixture(t))
}
