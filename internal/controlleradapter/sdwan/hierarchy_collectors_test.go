// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"os"
	"strings"
	"testing"
)

func TestHierarchyCollectorFixture(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/sdwan/network-hierarchy-collectors.yaml")
	if err != nil {
		t.Fatal(err)
	}
	rs, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 || rs[0].Type != "cflowd" || rs[1].Type != "security-logging" {
		t.Fatal("unexpected resources", rs)
	}
	for _, r := range rs {
		if r.Kind != "hierarchy-setting" || r.Section != "network_hierarchy" {
			t.Fatal("invalid collector resource", r)
		}
	}
	if _, err = Compile([]byte(strings.Replace(string(raw), "flow_active_timeout: 600", "flow_active_timeout: 1", 1))); err == nil {
		t.Fatal("invalid cflowd timeout accepted")
	}
	if _, err = Compile([]byte(strings.Replace(string(raw), "bfd_metrics_export: true", "bfd_metrics_export: false", 1))); err == nil {
		t.Fatal("export interval without BFD accepted")
	}
}
