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

package mapper

import (
	"testing"
	"time"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

func TestFlattenPathPreservesElemOrder(t *testing.T) {
	canonical, keys, originalOrder := FlattenPath(
		&gpb.Path{Elem: []*gpb.PathElem{
			{Name: "openconfig-interfaces:interfaces"},
			{Name: "interface", Key: map[string]string{"name": "GigabitEthernet1"}},
		}},
		&gpb.Path{Elem: []*gpb.PathElem{
			{Name: "state"},
			{Name: "admin-status"},
		}},
	)

	if !originalOrder {
		t.Fatal("originalOrder=false, want true")
	}
	want := "/interfaces/interface[name=GigabitEthernet1]/state/admin-status"
	if canonical != want {
		t.Fatalf("canonical=%q, want %q", canonical, want)
	}
	if len(keys) != 1 || keys[0] != (KeyValue{Key: "name", Value: "GigabitEthernet1"}) {
		t.Fatalf("keys=%+v, want name=GigabitEthernet1", keys)
	}
}

func TestFilterOrdering(t *testing.T) {
	notif := logNotification("/interfaces/interface/state/admin-status", "UP")
	aliases := []configv1alpha1.PathAlias{{
		Prefix: "/interfaces/interface/state/admin-status",
		Rename: "if.admin",
	}}

	t.Run("wire path filter sees canonical path before aliasing", func(t *testing.T) {
		events := New().Process(notif, EventContext{
			Subscription: "interfaces",
			Mapping: &configv1alpha1.MappingConfig{
				PathAliases: aliases,
				Filter: &configv1alpha1.FilterConfig{
					WirePath: &configv1alpha1.FilterRules{Deny: []string{"if.admin"}},
				},
			},
			Output: logsOnlyOutput(),
		})
		if len(events) != 1 {
			t.Fatalf("events=%+v, want one log event", events)
		}
		if events[0].Signal != SignalKindLog || events[0].Name != "if.admin" {
			t.Fatalf("event=%+v, want aliased log event", events[0])
		}
	})

	t.Run("metric name filter sees alias after aliasing", func(t *testing.T) {
		events := New().Process(notif, EventContext{
			Subscription: "interfaces",
			Mapping: &configv1alpha1.MappingConfig{
				PathAliases: aliases,
				Filter: &configv1alpha1.FilterConfig{
					MetricName: &configv1alpha1.FilterRules{Deny: []string{"if.admin"}},
				},
			},
			Output: logsOnlyOutput(),
		})
		if len(events) != 1 {
			t.Fatalf("events=%+v, want one drop event", events)
		}
		if events[0].Signal != SignalKindDrop || events[0].DropReason != DropReasonFilter {
			t.Fatalf("event=%+v, want filter drop", events[0])
		}
	})
}

func TestAliasLongestPrefixWins(t *testing.T) {
	resolver := NewAliasResolver([]configv1alpha1.PathAlias{
		{Prefix: "/interfaces", Rename: "net"},
		{Prefix: "/interfaces/interface/state", Rename: "if.state"},
	})

	got := resolver.Resolve("/interfaces/interface/state/admin-status")
	if got != "if.state/admin-status" {
		t.Fatalf("Resolve()=%q, want longest-prefix alias", got)
	}
}

func TestSeriesKeyCacheCapHit(t *testing.T) {
	cache := NewSeriesKeyCache(1)
	if known, accepted, size := cache.Check("sub\x00/a"); known || !accepted || size != 1 {
		t.Fatalf("first Check known=%t accepted=%t size=%d, want false true 1", known, accepted, size)
	}
	if known, accepted, size := cache.Check("sub\x00/a"); !known || !accepted || size != 1 {
		t.Fatalf("repeat Check known=%t accepted=%t size=%d, want true true 1", known, accepted, size)
	}
	if known, accepted, size := cache.Check("sub\x00/b"); known || accepted || size != 1 {
		t.Fatalf("cap Check known=%t accepted=%t size=%d, want false false 1", known, accepted, size)
	}
}

func TestResourceAttrExtractor(t *testing.T) {
	extractor := NewResourceAttrExtractor([]configv1alpha1.ResourceAttribute{{
		Path: "/system/state/hostname",
		Key:  "host.name",
	}})

	got := extractor.Extract(&gpb.Notification{
		Prefix: path("system", "state"),
		Update: []*gpb.Update{
			stringUpdate(path("hostname"), "edge-01"),
			stringUpdate(path("software-version"), "17.12"),
		},
	})
	if len(got) != 1 || got[0] != (KeyValue{Key: "host.name", Value: "edge-01"}) {
		t.Fatalf("Extract()=%+v, want host.name=edge-01", got)
	}
}

func TestTimestampPolicyCollectorVsDevice(t *testing.T) {
	deviceTS := time.Date(2026, 5, 9, 12, 0, 0, 123, time.UTC).UnixNano()
	receiveTS := time.Date(2026, 5, 9, 12, 1, 0, 0, time.UTC)
	notif := logNotification("/system/state/hostname", "edge-01")
	notif.Timestamp = deviceTS

	collectorEvents := New().Process(notif, EventContext{
		Subscription: "system",
		Output:       logsOnlyOutput(),
		ReceiveTime:  receiveTS,
	})
	if len(collectorEvents) != 1 {
		t.Fatalf("collector events=%+v, want one event", collectorEvents)
	}
	if !collectorEvents[0].Timestamp.Equal(receiveTS) {
		t.Fatalf("collector timestamp=%s, want %s", collectorEvents[0].Timestamp, receiveTS)
	}
	if got := attrValue(collectorEvents[0].Attributes, "cisco.device.timestamp"); got != "1778328000000000123" {
		t.Fatalf("cisco.device.timestamp=%q, want device timestamp attr", got)
	}

	useDevice := false
	deviceEvents := New().Process(notif, EventContext{
		Subscription: "system",
		Output:       logsOnlyOutput(),
		ReceiveTime:  receiveTS,
		Timestamps:   &configv1alpha1.TimestampConfig{UseCollectorTimestamp: &useDevice},
	})
	if len(deviceEvents) != 1 {
		t.Fatalf("device events=%+v, want one event", deviceEvents)
	}
	if want := time.Unix(0, deviceTS); !deviceEvents[0].Timestamp.Equal(want) {
		t.Fatalf("device timestamp=%s, want %s", deviceEvents[0].Timestamp, want)
	}
	if got := attrValue(deviceEvents[0].Attributes, "cisco.device.timestamp"); got != "" {
		t.Fatalf("cisco.device.timestamp=%q, want absent for device timestamp policy", got)
	}
}

func TestSeverityInference(t *testing.T) {
	tests := map[string]Severity{
		"Interface GigabitEthernet1 is UP": SeverityInfo,
		"Interface Loopback0 down":         SeverityWarn,
		"critical temperature error":       SeverityError,
		"ordinary state update":            SeverityInfo,
	}
	for body, want := range tests {
		if got := inferSeverity(body); got != want {
			t.Fatalf("inferSeverity(%q)=%s, want %s", body, got, want)
		}
	}
}

func TestOriginPropagation(t *testing.T) {
	events := New().Process(&gpb.Notification{
		Prefix: &gpb.Path{Origin: "openconfig", Elem: []*gpb.PathElem{{Name: "system"}, {Name: "state"}}},
		Update: []*gpb.Update{stringUpdate(path("hostname"), "edge-01")},
	}, EventContext{
		Device:       "edge-01",
		Subscription: "system",
		Output:       logsOnlyOutput(),
	})
	if len(events) != 1 {
		t.Fatalf("events=%+v, want one event", events)
	}
	if got := attrValue(events[0].Resource, "cisco.gnmi.origin"); got != "openconfig" {
		t.Fatalf("origin=%q, want openconfig", got)
	}
}

func logsOnlyOutput() configv1alpha1.OutputConfig {
	return configv1alpha1.OutputConfig{Signal: []string{configv1alpha1.TelemetrySignalLogs}}
}

func logNotification(canonicalPath, value string) *gpb.Notification {
	return &gpb.Notification{
		Update: []*gpb.Update{stringUpdate(pathFromCanonical(canonicalPath), value)},
	}
}

func stringUpdate(p *gpb.Path, value string) *gpb.Update {
	return &gpb.Update{
		Path: p,
		Val:  &gpb.TypedValue{Value: &gpb.TypedValue_StringVal{StringVal: value}},
	}
}

func path(names ...string) *gpb.Path {
	out := &gpb.Path{Elem: make([]*gpb.PathElem, 0, len(names))}
	for _, name := range names {
		out.Elem = append(out.Elem, &gpb.PathElem{Name: name})
	}
	return out
}

func pathFromCanonical(canonical string) *gpb.Path {
	p := normalizeCanonicalPath(canonical)
	if p == "/" {
		return &gpb.Path{}
	}
	parts := splitCanonical(p[1:])
	out := &gpb.Path{Elem: make([]*gpb.PathElem, 0, len(parts))}
	for _, part := range parts {
		out.Elem = append(out.Elem, &gpb.PathElem{Name: part})
	}
	return out
}

func splitCanonical(p string) []string {
	var out []string
	start := 0
	depth := 0
	for i, r := range p {
		switch r {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '/':
			if depth == 0 {
				out = append(out, p[start:i])
				start = i + 1
			}
		}
	}
	return append(out, p[start:])
}

func attrValue(attrs []KeyValue, key string) string {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value
		}
	}
	return ""
}

func TestPerEntityResourceAttributes(t *testing.T) {
	// One notification carries both a string leaf (state) and a numeric leaf
	// (cpu) for two apps. The resource attribute config matches the state
	// path with list-keys stripped — every metric event should land with the
	// state value of the SAME app, not the other one.
	notif := &gpb.Notification{
		Prefix: &gpb.Path{Origin: "rfc7951"},
		Update: []*gpb.Update{
			{
				Path: &gpb.Path{Elem: []*gpb.PathElem{
					{Name: "app-hosting-oper-data"},
					{Name: "app", Key: map[string]string{"name": "c9ktest"}},
					{Name: "details"}, {Name: "state"},
				}},
				Val: &gpb.TypedValue{Value: &gpb.TypedValue_StringVal{StringVal: "DEPLOYED"}},
			},
			{
				Path: &gpb.Path{Elem: []*gpb.PathElem{
					{Name: "app-hosting-oper-data"},
					{Name: "app", Key: map[string]string{"name": "c9ktest"}},
					{Name: "details"}, {Name: "resource-reservation"}, {Name: "cpu"},
				}},
				Val: &gpb.TypedValue{Value: &gpb.TypedValue_UintVal{UintVal: 1480}},
			},
			{
				Path: &gpb.Path{Elem: []*gpb.PathElem{
					{Name: "app-hosting-oper-data"},
					{Name: "app", Key: map[string]string{"name": "cvk0000"}},
					{Name: "details"}, {Name: "state"},
				}},
				Val: &gpb.TypedValue{Value: &gpb.TypedValue_StringVal{StringVal: "RUNNING"}},
			},
			{
				Path: &gpb.Path{Elem: []*gpb.PathElem{
					{Name: "app-hosting-oper-data"},
					{Name: "app", Key: map[string]string{"name": "cvk0000"}},
					{Name: "details"}, {Name: "resource-reservation"}, {Name: "cpu"},
				}},
				Val: &gpb.TypedValue{Value: &gpb.TypedValue_UintVal{UintVal: 500}},
			},
		},
	}

	mapping := &configv1alpha1.MappingConfig{
		ResourceAttributes: []configv1alpha1.ResourceAttribute{
			{Path: "/app-hosting-oper-data/app/details/state", Key: "cisco.app_hosting.state"},
		},
	}
	out := configv1alpha1.OutputConfig{Signal: []string{"metrics", "logs"}}

	mapper := New()
	events := mapper.Process(notif, EventContext{
		Device: "cat9k", Subscription: "ah", StreamEpoch: 1,
		Mapping: mapping, Output: out, ReceiveTime: time.Now(),
	})

	// Find the cpu metric events for each app and verify per-entity state attr.
	wantState := map[string]string{"c9ktest": "DEPLOYED", "cvk0000": "RUNNING"}
	seen := map[string]string{}
	for _, ev := range events {
		if ev.Signal != SignalKindMetric {
			continue
		}
		var app string
		for _, kv := range ev.Attributes {
			if kv.Key == "name" {
				app = kv.Value
			}
		}
		if app == "" {
			continue
		}
		for _, kv := range ev.Resource {
			if kv.Key == "cisco.app_hosting.state" {
				seen[app] = kv.Value
			}
		}
	}
	for app, want := range wantState {
		if got := seen[app]; got != want {
			t.Errorf("app=%s state=%q, want %q (resources observed: %+v)", app, got, want, seen)
		}
	}
}
