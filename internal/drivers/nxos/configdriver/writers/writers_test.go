// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package writers

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
)

func TestSystemDiffEmitsHostnameDME(t *testing.T) {
	w := Get("system")
	ops, err := w.Diff(map[string]any{"hostname": "leaf-01"}, map[string]any{"hostname": "old"})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("ops=%#v", ops)
	}
	body := requireDMEOp(t, ops[0], transport.VerbMerge, nxosschema.DNSystem)
	top, ok := body["topSystem"].(map[string]any)
	if !ok {
		t.Fatalf("body=%#v", body)
	}
	attrs, _ := top["attributes"].(map[string]any)
	if attrs["name"] != "leaf-01" {
		t.Fatalf("attrs=%#v", attrs)
	}
}

func TestVLANDiffEmitsNameDME(t *testing.T) {
	w := Get("vlan")
	ops, err := w.Diff(
		map[string]any{"vlans": []any{map[string]any{"id": 101, "name": "cvk_probe"}}},
		map[string]any{"vlans": []any{map[string]any{"id": 101, "name": "old"}}},
	)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("ops=%#v", ops)
	}
	body := requireDMEOp(t, ops[0], transport.VerbMerge, nxosschema.DNBridgeDomain)
	raw := mustJSON(t, body)
	for _, want := range []string{`"bdEntity"`, `"l2BD"`, `"fabEncap":"vlan-101"`, `"name":"cvk_probe"`, `"pcTag":"1"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("payload %s missing %s", raw, want)
		}
	}
}

func TestEthernetDiffEmitsDescriptionAndShutdownDME(t *testing.T) {
	w := Get("interface_ethernet")
	ops, err := w.Diff(
		map[string]any{"interfaces": []any{map[string]any{"type": "Ethernet", "name": "1/1", "description": "uplink", "shutdown": false}}},
		map[string]any{"interfaces": []any{map[string]any{"type": "Ethernet", "name": "1/1", "description": "old", "shutdown": true}}},
	)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("ops=%#v", ops)
	}
	body := requireDMEOp(t, ops[0], transport.VerbMerge, nxosschema.DNInterfaceEntity)
	raw := mustJSON(t, body)
	for _, want := range []string{`"interfaceEntity"`, `"l1PhysIf"`, `"id":"eth1/1"`, `"descr":"uplink"`, `"adminSt":"up"`, `"userCfgdFlags":"admin_state"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("payload %s missing %s", raw, want)
		}
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

func TestEthernetDiffEmptyDescriptionClearsDescr(t *testing.T) {
	w := Get("interface_ethernet")
	ops, err := w.Diff(
		map[string]any{"interfaces": []any{map[string]any{"type": "Ethernet", "name": "1/1", "description": ""}}},
		map[string]any{"interfaces": []any{map[string]any{"type": "Ethernet", "name": "1/1", "description": "old"}}},
	)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("ops=%#v", ops)
	}
	raw := mustJSON(t, requireDMEOp(t, ops[0], transport.VerbMerge, nxosschema.DNInterfaceEntity))
	if !strings.Contains(raw, `"descr":""`) {
		t.Fatalf("payload %s missing empty descr", raw)
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

func requireDMEOp(t *testing.T, op transport.Op, verb transport.Verb, path string) map[string]any {
	t.Helper()
	if op.Verb != verb {
		t.Fatalf("verb=%s, want %s", op.Verb, verb)
	}
	if op.Path != path {
		t.Fatalf("path=%q, want %q", op.Path, path)
	}
	var body map[string]any
	if err := json.Unmarshal(op.Body, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return body
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}
