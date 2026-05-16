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
	"testing"
)

func TestVersionOverride_versionInRange(t *testing.T) {
	o := VersionOverride{
		MinVersion: [2]int{17, 0},
		MaxVersion: [2]int{17, 18},
	}
	tests := []struct {
		major, minor int
		want         bool
	}{
		{16, 99, false}, // below min
		{17, 0, true},   // exactly min
		{17, 15, true},  // in range
		{17, 16, true},  // in range
		{17, 17, true},  // just below max
		{17, 18, false}, // exactly max (exclusive)
		{17, 19, false}, // above max
		{18, 0, false},  // above max major
	}
	for _, tt := range tests {
		got := o.versionInRange(tt.major, tt.minor)
		if got != tt.want {
			t.Errorf("versionInRange(%d, %d) = %v, want %v", tt.major, tt.minor, got, tt.want)
		}
	}
}

func TestResolveForVersion_routeMap(t *testing.T) {
	// Simulate 17.16 device
	r := NewOverrideResolverForMajorMinor(17, 16)
	o, ok := r.GetOverride("route_map")
	if !ok {
		t.Fatal("expected override for route_map on 17.16, got nil")
	}
	if o.NestedYANGInnerOverride != "Cisco-IOS-XE-route-map:route-map-seq" {
		t.Errorf("NestedYANGInnerOverride = %q, want %q",
			o.NestedYANGInnerOverride, "Cisco-IOS-XE-route-map:route-map-seq")
	}

	// Simulate 17.18 device — no override expected
	r = NewOverrideResolverForMajorMinor(17, 18)
	if o, ok := r.GetOverride("route_map"); ok {
		t.Errorf("expected no override for route_map on 17.18, got %+v", o)
	}
}

func TestResolveForVersion_ntp(t *testing.T) {
	r := NewOverrideResolverForMajorMinor(17, 15)
	o, ok := r.GetOverride("ntp")
	if !ok {
		t.Fatal("expected override for ntp on 17.15, got nil")
	}
	if len(o.EmptyLeaves) != 1 || o.EmptyLeaves[0] != "prefer" {
		t.Errorf("EmptyLeaves = %v, want [prefer]", o.EmptyLeaves)
	}
}

func TestResolveForVersion_bgpOverride(t *testing.T) {
	// BGP has an override entry on < 17.18 for path/envelope
	// selection; transform logic is in the writer.
	r := NewOverrideResolverForMajorMinor(17, 16)
	o, ok := r.GetOverride("bgp")
	if !ok {
		t.Fatal("expected override for bgp on 17.16, got nil")
	}
	if o.YANGPathOverride != bgpYANGPathLegacy {
		t.Errorf("YANGPathOverride = %q, want %q", o.YANGPathOverride, bgpYANGPathLegacy)
	}
	if o.EnvelopeKeyOverride != bgpEnvelopeKeyLegacy {
		t.Errorf("EnvelopeKeyOverride = %q, want %q", o.EnvelopeKeyOverride, bgpEnvelopeKeyLegacy)
	}

	// No override on 17.18.
	r = NewOverrideResolverForMajorMinor(17, 18)
	if o, ok := r.GetOverride("bgp"); ok {
		t.Errorf("expected no override for bgp on 17.18, got %+v", o)
	}
}

func TestResolveForVersion_noOverrideOn1718(t *testing.T) {
	r := NewOverrideResolverForMajorMinor(17, 18)
	families := []string{
		"route_map", "ntp", "logging", "snmp_server",
		"access_list_extended", "spanning_tree",
	}
	for _, f := range families {
		if o, ok := r.GetOverride(f); ok {
			t.Errorf("expected no override for %s on 17.18, got %+v", f, o)
		}
	}
}

func TestApplyElementMap(t *testing.T) {
	body := map[string]any{
		"name": "MY-MAP",
		"route-map-without-order-seq": []any{
			map[string]any{"seq": 10, "operation": "permit"},
		},
	}
	emap := map[string]string{
		"route-map-without-order-seq": "Cisco-IOS-XE-route-map:route-map-seq",
	}
	result := ApplyElementMap(body, emap)

	if _, ok := result["route-map-without-order-seq"]; ok {
		t.Error("expected old key to be removed")
	}
	if _, ok := result["Cisco-IOS-XE-route-map:route-map-seq"]; !ok {
		t.Error("expected new key to be present")
	}
	if result["name"] != "MY-MAP" {
		t.Errorf("name = %v, want MY-MAP", result["name"])
	}
}

func TestApplyElementMap_recursive(t *testing.T) {
	body := map[string]any{
		"outer": map[string]any{
			"RO": []any{nil},
		},
	}
	emap := map[string]string{
		"RO": "Cisco-IOS-XE-snmp:RO",
	}
	result := ApplyElementMap(body, emap)
	inner := result["outer"].(map[string]any)
	if _, ok := inner["RO"]; ok {
		t.Error("expected old key RO to be removed in nested map")
	}
	if _, ok := inner["Cisco-IOS-XE-snmp:RO"]; !ok {
		t.Error("expected prefixed key to be present in nested map")
	}
}

func TestApplyEmptyLeaves(t *testing.T) {
	body := map[string]any{
		"ip-address": "10.1.1.1",
		"prefer":     true,
		"version":    4,
	}
	result := ApplyEmptyLeaves(body, []string{"prefer"})
	pref, ok := result["prefer"]
	if !ok {
		t.Fatal("expected prefer key to be present")
	}
	arr, ok := pref.([]any)
	if !ok || len(arr) != 1 || arr[0] != nil {
		t.Errorf("prefer = %v, want [null]", pref)
	}
	if result["ip-address"] != "10.1.1.1" {
		t.Errorf("ip-address should be unchanged")
	}
}

func TestApplyEmptyLeaves_false(t *testing.T) {
	body := map[string]any{
		"prefer": false,
	}
	result := ApplyEmptyLeaves(body, []string{"prefer"})
	if _, ok := result["prefer"]; ok {
		t.Error("expected prefer to be removed when false")
	}
}

func TestApplyOverrideToBody_fullChain(t *testing.T) {
	o := &VersionOverride{
		ElementMap:  map[string]string{"old-key": "new-key"},
		EmptyLeaves: []string{"prefer"},
		BodyTransform: func(body map[string]any) map[string]any {
			body["extra"] = true
			return body
		},
	}
	body := map[string]any{
		"old-key": "value",
		"prefer":  true,
		"keep":    42,
	}
	result := ApplyOverrideToBody(body, o)
	if _, ok := result["old-key"]; ok {
		t.Error("old-key should have been renamed")
	}
	if result["new-key"] != "value" {
		t.Error("new-key should have the value")
	}
	pref := result["prefer"].([]any)
	if len(pref) != 1 || pref[0] != nil {
		t.Errorf("prefer = %v, want [null]", pref)
	}
	if result["extra"] != true {
		t.Error("BodyTransform should have added 'extra'")
	}
}

func TestResolvedNestedYANGInner(t *testing.T) {
	r := NewOverrideResolverForMajorMinor(17, 16)
	got := r.ResolvedNestedYANGInner("route_map", "route-map-without-order-seq")
	want := "Cisco-IOS-XE-route-map:route-map-seq"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	r = NewOverrideResolverForMajorMinor(17, 18)
	got = r.ResolvedNestedYANGInner("route_map", "route-map-without-order-seq")
	if got != "route-map-without-order-seq" {
		t.Errorf("got %q, want default on 17.18", got)
	}
}

func TestNTPOverride_emptyLeafInKeyedWriter(t *testing.T) {
	// Simulate what keyedListWriter.Diff does for NTP on 17.16:
	// after ntpServerToYANG, we get {ip-address: "10.1.1.1", prefer: true}
	// The override should convert prefer: true → prefer: [null]
	r := NewOverrideResolverForMajorMinor(17, 16)

	proj := map[string]any{
		"ip-address": "10.1.1.1",
		"prefer":     true,
	}
	if o, ok := r.GetOverride("ntp"); ok {
		proj = ApplyOverrideToBody(proj, o)
	}

	pref, ok := proj["prefer"]
	if !ok {
		t.Fatal("prefer should be present")
	}
	arr, ok := pref.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("prefer = %T(%v), want []any{nil}", pref, pref)
	}

	// Verify JSON output matches YANG empty-leaf encoding
	b, _ := json.Marshal(proj)
	expected := `{"ip-address":"10.1.1.1","prefer":[null]}`
	if string(b) != expected {
		t.Errorf("JSON = %s, want %s", string(b), expected)
	}
}
