// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package writers

import (
	"reflect"
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

func TestNXOSWritersNoOpWhenDesiredMatchesObserved(t *testing.T) {
	tests := []struct {
		name     string
		family   string
		desired  any
		observed any
	}{
		{
			name:     "system",
			family:   "system",
			desired:  map[string]any{"hostname": "leaf-01"},
			observed: map[string]any{"hostname": "leaf-01"},
		},
		{
			name:     "vlan",
			family:   "vlan",
			desired:  map[string]any{"vlans": []any{map[string]any{"id": 101, "name": "cvk_probe"}}},
			observed: map[string]any{"vlans": []any{map[string]any{"id": 101, "name": "cvk_probe"}}},
		},
		{
			name:     "interface_ethernet",
			family:   "interface_ethernet",
			desired:  map[string]any{"interfaces": []any{map[string]any{"type": "Ethernet", "name": "1/1", "description": "uplink", "shutdown": false}}},
			observed: map[string]any{"interfaces": []any{map[string]any{"type": "Ethernet", "name": "1/1", "description": "uplink", "shutdown": false}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops, err := Get(tt.family).Diff(tt.desired, tt.observed)
			if err != nil {
				t.Fatalf("Diff: %v", err)
			}
			if len(ops) != 0 {
				t.Fatalf("ops=%#v, want no-op", ops)
			}
		})
	}
}

func TestVLANDiffRejectsInvalidInput(t *testing.T) {
	w := Get("vlan")
	if _, err := w.Diff(map[string]any{"vlans": []any{map[string]any{"id": 4095, "name": "too_high"}}}, nil); err == nil {
		t.Fatal("Diff accepted out-of-range VLAN id")
	}
	if _, err := w.Diff(map[string]any{"vlans": []any{map[string]any{"id": 101, "name": ""}}}, nil); err == nil {
		t.Fatal("Diff accepted empty VLAN name")
	}
}

func TestEthernetDiffEmptyDescriptionEmitsNoDescription(t *testing.T) {
	w := Get("interface_ethernet")
	ops, err := w.Diff(
		map[string]any{"interfaces": []any{map[string]any{"type": "Ethernet", "name": "1/1", "description": ""}}},
		map[string]any{"interfaces": []any{map[string]any{"type": "Ethernet", "name": "1/1", "description": "old"}}},
	)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 || !strings.Contains(string(ops[0].Body), "no description") {
		t.Fatalf("ops=%#v", ops)
	}
}

func TestNXOSWritersKeysOf(t *testing.T) {
	keyer, ok := Get("vlan").(interface{ KeysOf(any) []string })
	if !ok {
		t.Fatal("vlan writer does not expose KeysOf")
	}
	if got, want := keyer.KeysOf(map[string]any{"vlans": []any{
		map[string]any{"id": 101},
		map[string]any{"id": "102"},
	}}), []string{"101", "102"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("vlan KeysOf=%#v, want %#v", got, want)
	}

	ethKeyer, ok := Get("interface_ethernet").(interface{ KeysOf(any) []string })
	if !ok {
		t.Fatal("interface_ethernet writer does not expose KeysOf")
	}
	if got, want := ethKeyer.KeysOf(map[string]any{"interfaces": []any{
		map[string]any{"type": "Ethernet", "name": "1/1"},
		map[string]any{"type": "Ethernet", "name": "Ethernet1/2"},
	}}), []string{"Ethernet1/1", "Ethernet1/2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("interface KeysOf=%#v, want %#v", got, want)
	}
}
