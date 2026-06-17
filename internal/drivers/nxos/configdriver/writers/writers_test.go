// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package writers

import (
	"strings"
	"testing"
)

func TestSystemDiffEmitsHostnameCLI(t *testing.T) {
	w := Get("system")
	ops, err := w.Diff(map[string]any{"hostname": "leaf-01"}, map[string]any{"hostname": "old"})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 || !strings.Contains(string(ops[0].Body), "hostname leaf-01") {
		t.Fatalf("ops=%#v", ops)
	}
}

func TestVLANDiffEmitsNameCLI(t *testing.T) {
	w := Get("vlan")
	ops, err := w.Diff(
		map[string]any{"vlans": []any{map[string]any{"id": 101, "name": "cvk_probe"}}},
		map[string]any{"vlans": []any{map[string]any{"id": 101, "name": "old"}}},
	)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 || !strings.Contains(string(ops[0].Body), "vlan 101") || !strings.Contains(string(ops[0].Body), "name cvk_probe") {
		t.Fatalf("ops=%#v", ops)
	}
}

func TestEthernetDiffEmitsDescriptionAndShutdownCLI(t *testing.T) {
	w := Get("interface_ethernet")
	ops, err := w.Diff(
		map[string]any{"interfaces": []any{map[string]any{"type": "Ethernet", "name": "1/1", "description": "uplink", "shutdown": false}}},
		map[string]any{"interfaces": []any{map[string]any{"type": "Ethernet", "name": "1/1", "description": "old", "shutdown": true}}},
	)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	body := string(ops[0].Body)
	if len(ops) != 1 || !strings.Contains(body, "interface Ethernet1/1") || !strings.Contains(body, "description uplink") || !strings.Contains(body, "no shutdown") {
		t.Fatalf("ops=%#v", ops)
	}
}
