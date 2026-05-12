// Copyright 2026 Cisco Systems Inc.
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
	"math"
	"testing"
	"time"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/classifier"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

func TestMapperEmitsMetricsForNumericLeaves(t *testing.T) {
	tests := []struct {
		name string
		val  *gpb.TypedValue
		want float64
	}{
		{
			name: "int",
			val:  &gpb.TypedValue{Value: &gpb.TypedValue_IntVal{IntVal: -42}},
			want: -42,
		},
		{
			name: "uint",
			val:  &gpb.TypedValue{Value: &gpb.TypedValue_UintVal{UintVal: 42}},
			want: 42,
		},
		{
			name: "float",
			val:  &gpb.TypedValue{Value: &gpb.TypedValue_FloatVal{FloatVal: 12.5}},
			want: 12.5,
		},
		{
			name: "decimal",
			val:  &gpb.TypedValue{Value: &gpb.TypedValue_DecimalVal{DecimalVal: &gpb.Decimal64{Digits: 12345, Precision: 2}}},
			want: 123.45,
		},
		{
			name: "json",
			val:  &gpb.TypedValue{Value: &gpb.TypedValue_JsonVal{JsonVal: []byte("10.25")}},
			want: 10.25,
		},
		{
			name: "json_ietf",
			val:  &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: []byte("20.5")}},
			want: 20.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := New().Process(&gpb.Notification{
				Update: []*gpb.Update{{
					Path: pathFromCanonical("/system/state/" + tt.name),
					Val:  tt.val,
				}},
			}, EventContext{
				Subscription: "system",
				Output:       metricsOnlyOutput(),
			})
			if len(events) != 1 {
				t.Fatalf("events=%+v, want one metric event", events)
			}
			event := events[0]
			if event.Signal != SignalKindMetric {
				t.Fatalf("Signal=%s, want metrics", event.Signal)
			}
			if event.NumberValue == nil || math.Abs(*event.NumberValue-tt.want) > 0.000001 {
				t.Fatalf("NumberValue=%v, want %f", event.NumberValue, tt.want)
			}
			if event.MetricKind != classifier.MetricKindGauge {
				t.Fatalf("MetricKind=%s, want gauge fallback", event.MetricKind)
			}
		})
	}
}

func TestStartTimestampPersistsAcrossSamples(t *testing.T) {
	m := New()
	firstReceive := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	secondReceive := firstReceive.Add(time.Minute)
	ctx := EventContext{
		Subscription: "interfaces",
		Output:       metricsOnlyOutput(),
		StreamEpoch:  7,
		ReceiveTime:  firstReceive,
	}

	first := m.Process(counterNotification(100), ctx)
	ctx.ReceiveTime = secondReceive
	second := m.Process(counterNotification(200), ctx)

	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("first=%+v second=%+v, want one event each", first, second)
	}
	if first[0].MetricKind != classifier.MetricKindSum {
		t.Fatalf("MetricKind=%s, want sum", first[0].MetricKind)
	}
	if !first[0].StartTimestamp.Equal(firstReceive) {
		t.Fatalf("first StartTimestamp=%s, want %s", first[0].StartTimestamp, firstReceive)
	}
	if !second[0].StartTimestamp.Equal(firstReceive) {
		t.Fatalf("second StartTimestamp=%s, want persisted %s", second[0].StartTimestamp, firstReceive)
	}
}

func TestStartTimestampResetsOnEpochChange(t *testing.T) {
	m := New()
	firstReceive := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	secondReceive := firstReceive.Add(time.Minute)

	first := m.Process(counterNotification(100), EventContext{
		Subscription: "interfaces",
		Output:       metricsOnlyOutput(),
		StreamEpoch:  7,
		ReceiveTime:  firstReceive,
	})
	second := m.Process(counterNotification(200), EventContext{
		Subscription: "interfaces",
		Output:       metricsOnlyOutput(),
		StreamEpoch:  8,
		ReceiveTime:  secondReceive,
	})

	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("first=%+v second=%+v, want one event each", first, second)
	}
	if !first[0].StartTimestamp.Equal(firstReceive) {
		t.Fatalf("first StartTimestamp=%s, want %s", first[0].StartTimestamp, firstReceive)
	}
	if !second[0].StartTimestamp.Equal(secondReceive) {
		t.Fatalf("second StartTimestamp=%s, want reset %s", second[0].StartTimestamp, secondReceive)
	}
}

func metricsOnlyOutput() configv1alpha1.OutputConfig {
	return configv1alpha1.OutputConfig{Signal: []string{configv1alpha1.TelemetrySignalMetrics}}
}

func counterNotification(value uint64) *gpb.Notification {
	return &gpb.Notification{
		Update: []*gpb.Update{{
			Path: interfaceCounterPath("in-octets"),
			Val:  &gpb.TypedValue{Value: &gpb.TypedValue_UintVal{UintVal: value}},
		}},
	}
}

func interfaceCounterPath(leaf string) *gpb.Path {
	return &gpb.Path{Elem: []*gpb.PathElem{
		{Name: "interfaces"},
		{Name: "interface", Key: map[string]string{"name": "GigabitEthernet1"}},
		{Name: "state"},
		{Name: "counters"},
		{Name: leaf},
	}}
}
