// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"os"
	"strings"
	"testing"
)

func classicSystemFixture(t *testing.T) []Resource {
	t.Helper()
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-system.yaml")
	if err != nil {
		t.Fatal(err)
	}
	rs, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}
func TestClassicSystemFixture(t *testing.T) {
	rs := classicSystemFixture(t)
	if len(rs) != 1 || rs[0].Type != "cisco_system" {
		t.Fatal("invalid system fixture")
	}
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-system.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Compile([]byte(strings.Replace(string(raw), "hostname_variable: cvkHostname", "enhanced_app_aware_routing: true", 1))); err == nil {
		t.Fatal("unmapped EPFR accepted")
	}
	group := strings.Replace(string(raw), "        timezone: UTC", "        timezone: UTC\n        endpoint_trackers:\n          - name: cvk-group\n            group_trackers: [one, two]", 1)
	groupResources, err := Compile([]byte(group))
	if err != nil {
		t.Fatal(err)
	}
	definition := groupResources[0].Body["templateDefinition"].(map[string]any)
	if definition["tracker"] == nil {
		t.Fatal("endpoint tracker mapping missing")
	}
}
func TestLiveClassicSystemLifecycle(t *testing.T) { liveParcelLifecycle(t, classicSystemFixture(t)) }
