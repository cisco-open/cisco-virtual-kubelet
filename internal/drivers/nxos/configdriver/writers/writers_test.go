// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package writers

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	enginewriters "github.com/cisco/virtual-kubelet-cisco/internal/configengine/writers"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
)

func TestSystemDiffEmitsHostnameDME(t *testing.T) {
	w := Get("system")
	ops, err := w.Diff(map[string]any{"hostname": "leaf-01", "mtu": 9216}, map[string]any{"hostname": "old", "mtu": 1500})
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
	raw := mustJSON(t, body)
	for _, want := range []string{`"ethpmEntity"`, `"ethpmInst"`, `"systemJumboMtu":"9216"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("payload %s missing %s", raw, want)
		}
	}
}

func TestFeatureDiffEmitsDME(t *testing.T) {
	w := Get("feature")
	ops, err := w.Diff(
		map[string]any{
			"lldp":                  true,
			"bgp":                   false,
			"fabric_forwarding":     true,
			"private_vlan":          true,
			"vn_segment_vlan_based": true,
		},
		map[string]any{
			"lldp":                  false,
			"bgp":                   true,
			"fabric_forwarding":     false,
			"private_vlan":          false,
			"vn_segment_vlan_based": false,
		},
	)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("ops=%#v", ops)
	}
	raw := mustJSON(t, requireDMEOp(t, ops[0], transport.VerbMerge, nxosschema.DNSystem))
	for _, want := range []string{
		`"topSystem"`,
		`"fmEntity"`,
		`"fmLldp"`,
		`"fmBgp"`,
		`"fmHmm"`,
		`"fmPvlan"`,
		`"fmVnSegment"`,
		`"adminSt":"enabled"`,
		`"adminSt":"disabled"`,
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("payload %s missing %s", raw, want)
		}
	}
}

func TestFeatureSetDiffEmitsDME(t *testing.T) {
	w := Get("feature_set")
	ops, err := w.Diff(
		map[string]any{"fex": true, "mpls": false, "virtualization": "installed"},
		map[string]any{"fex": false, "mpls": true, "virtualization": "uninstalled"},
	)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("ops=%#v", ops)
	}
	raw := mustJSON(t, requireDMEOp(t, ops[0], transport.VerbMerge, nxosschema.DNSystem))
	for _, want := range []string{
		`"fsetFeatureSet"`,
		`"name":"fex"`,
		`"name":"mpls"`,
		`"name":"virtualization"`,
		`"adminSt":"enabled"`,
		`"adminSt":"disabled"`,
		`"adminSt":"installed"`,
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("payload %s missing %s", raw, want)
		}
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
	for _, want := range []string{`"bdEntity"`, `"l2BD"`, `"fabEncap":"vlan-101"`, `"name":"cvk_probe"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("payload %s missing %s", raw, want)
		}
	}
	if strings.Contains(raw, `"pcTag"`) {
		t.Fatalf("payload %s contains pcTag, which is not emitted by the pinned provider", raw)
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

func TestEthernetDiffAcceptsNetAsCodeID(t *testing.T) {
	w := Get("interface_ethernet")
	ops, err := w.Diff(
		map[string]any{"interfaces": []any{map[string]any{"id": "1/1", "description": "uplink", "mtu": 9216}}},
		map[string]any{"interfaces": []any{map[string]any{"name": "1/1", "description": "old", "mtu": 1500}}},
	)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("ops=%#v", ops)
	}
	raw := mustJSON(t, requireDMEOp(t, ops[0], transport.VerbMerge, nxosschema.DNInterfaceEntity))
	for _, want := range []string{`"id":"eth1/1"`, `"descr":"uplink"`, `"mtu":"9216"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("payload %s missing %s", raw, want)
		}
	}
}

func TestNativeEthernetDescriptionUpdatePreservesOmittedAdminAndLayer(t *testing.T) {
	ops, err := Get("interface_ethernet").Diff(
		map[string]any{"interfaces": []any{map[string]any{"id": "1/1", "description": "new"}}},
		map[string]any{"interfaces": []any{map[string]any{
			"name": "1/1", "description": "old", "shutdown": true, "layer": "Layer3",
		}}},
	)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("ops=%#v", ops)
	}
	raw := mustJSON(t, requireDMEOp(t, ops[0], transport.VerbMerge, nxosschema.DNInterfaceEntity))
	for _, forbidden := range []string{`"adminSt"`, `"layer"`, `"userCfgdFlags"`} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("native description-only payload %s unexpectedly owns %s", raw, forbidden)
		}
	}
}

func TestStrictNetAsCodeEthernetAppliesProviderDefaults(t *testing.T) {
	w := Get("interface_ethernet")
	ops, err := enginewriters.Diff(enginewriters.DiffContext{
		Platform: "nxos", ModelVersion: "0.3.0", DeviceVersion: "10.3(9)",
	}, w,
		map[string]any{"interfaces": []any{map[string]any{"id": "1/1", "description": "new"}}},
		map[string]any{"interfaces": []any{map[string]any{
			"name": "1/1", "description": "old", "shutdown": false, "layer": "Layer2",
			"user_configured_flags": "admin_layer",
		}}},
	)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("ops=%#v", ops)
	}
	raw := mustJSON(t, requireDMEOp(t, ops[0], transport.VerbMerge, nxosschema.DNInterfaceEntity))
	for _, want := range []string{`"adminSt":"up"`, `"layer":"Layer2"`, `"userCfgdFlags":"admin_layer"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("strict provider payload %s missing %s", raw, want)
		}
	}
}

func TestStrictNetAsCodeEthernetRefusesUnsafeImplicitConversion(t *testing.T) {
	w := Get("interface_ethernet")
	ctx := enginewriters.DiffContext{Platform: "nxos", ModelVersion: "0.3.0", DeviceVersion: "10.3(9)"}
	for name, observed := range map[string]map[string]any{
		"layer3":   {"name": "1/1", "shutdown": false, "layer": "Layer3"},
		"shutdown": {"name": "1/1", "shutdown": true, "layer": "Layer2"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := enginewriters.Diff(ctx, w,
				map[string]any{"interfaces": []any{map[string]any{"id": "1/1", "description": "new"}}},
				map[string]any{"interfaces": []any{observed}},
			)
			if err == nil {
				t.Fatal("strict Diff accepted unsafe implicit interface conversion")
			}
		})
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
			desired:  map[string]any{"hostname": "leaf-01", "mtu": 9216},
			observed: map[string]any{"hostname": "leaf-01", "mtu": 9216},
		},
		{
			name:     "feature",
			family:   "feature",
			desired:  map[string]any{"lldp": true, "bgp": false},
			observed: map[string]any{"lldp": true, "bgp": false},
		},
		{
			name:     "feature_set",
			family:   "feature_set",
			desired:  map[string]any{"fex": true, "mpls": false},
			observed: map[string]any{"fex": true, "mpls": false},
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
	if _, err := w.Diff(map[string]any{"vlans": []any{map[string]any{"id": 101, "vni": 10101}}}, nil); err == nil {
		t.Fatal("Diff accepted unsupported VLAN VNI")
	}
}

func TestEthernetDiffRejectsInvalidMTU(t *testing.T) {
	w := Get("interface_ethernet")
	if _, err := w.Diff(map[string]any{"interfaces": []any{
		map[string]any{"id": "1/1", "mtu": 100},
	}}, nil); err == nil {
		t.Fatal("Diff accepted invalid MTU")
	}
}

func TestSystemDiffRejectsInvalidMTU(t *testing.T) {
	w := Get("system")
	if _, err := w.Diff(map[string]any{"mtu": 100}, nil); err == nil {
		t.Fatal("Diff accepted invalid system MTU")
	}
}

func TestNXOSWritersRejectUnsupportedFields(t *testing.T) {
	tests := []struct {
		name    string
		family  string
		desired any
	}{
		{
			name:    "system-clock",
			family:  "system",
			desired: map[string]any{"hostname": "leaf-01", "clock": map[string]any{"timezone_name": "UTC"}},
		},
		{
			name:   "ethernet-switchport",
			family: "interface_ethernet",
			desired: map[string]any{"interfaces": []any{
				map[string]any{"id": "1/1", "switchport": map[string]any{"enabled": false}},
			}},
		},
		{
			name:    "feature-provider-alias",
			family:  "feature",
			desired: map[string]any{"hmm": true},
		},
		{
			name:    "feature-set-unknown",
			family:  "feature_set",
			desired: map[string]any{"fabric": true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Get(tt.family).Diff(tt.desired, nil); err == nil {
				t.Fatalf("Diff accepted unsupported fields for %s", tt.family)
			}
		})
	}
}

func TestFeatureDiffRejectsDisablingProtectedManagementAccess(t *testing.T) {
	w := Get("feature")
	for _, field := range []string{"nxapi", "ssh", "scp_server", "sftp_server", "tacacs"} {
		t.Run(field, func(t *testing.T) {
			if _, err := w.Diff(map[string]any{field: false}, map[string]any{field: true}); err == nil {
				t.Fatalf("Diff accepted disabling protected feature.%s", field)
			}
			if _, err := w.Diff(map[string]any{field: "disabled"}, map[string]any{field: true}); err == nil {
				t.Fatalf("Diff accepted string disabled for protected feature.%s", field)
			}
		})
	}
}

func TestFeatureDiffAllowsNonManagementFeatureDisable(t *testing.T) {
	w := Get("feature")
	ops, err := w.Diff(map[string]any{"bgp": false}, map[string]any{"bgp": true})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("ops=%#v, want BGP disable op", ops)
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

func TestVLANPruneDiffEmitsScopedDMEDeletes(t *testing.T) {
	pruner, ok := Get("vlan").(interface {
		PruneDiff(any, any) ([]transport.Op, error)
	})
	if !ok {
		t.Fatal("vlan writer does not expose PruneDiff")
	}
	ops, err := pruner.PruneDiff(
		map[string]any{"vlans": []any{map[string]any{"id": 101}}},
		map[string]any{"vlans": []any{
			map[string]any{"id": 103},
			map[string]any{"id": 101},
			map[string]any{"id": 102},
		}},
	)
	if err != nil {
		t.Fatalf("PruneDiff: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("ops=%#v, want two deletes", ops)
	}
	for i, wantPath := range []string{
		nxosschema.DNBridgeDomain + "/bd-[vlan-102]",
		nxosschema.DNBridgeDomain + "/bd-[vlan-103]",
	} {
		if ops[i].Verb != transport.VerbDelete {
			t.Fatalf("op[%d] verb=%s, want DELETE", i, ops[i].Verb)
		}
		if ops[i].Path != wantPath {
			t.Fatalf("op[%d] path=%q, want %q", i, ops[i].Path, wantPath)
		}
		if len(ops[i].Body) != 0 {
			t.Fatalf("op[%d] body=%s, want empty", i, string(ops[i].Body))
		}
	}
}

func TestVLANPruneDiffRejectsDefaultVLANDelete(t *testing.T) {
	pruner := Get("vlan").(interface {
		PruneDiff(any, any) ([]transport.Op, error)
	})
	if _, err := pruner.PruneDiff(
		map[string]any{"vlans": []any{}},
		map[string]any{"vlans": []any{map[string]any{"id": 1}}},
	); err == nil {
		t.Fatal("PruneDiff accepted deleting VLAN 1")
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
