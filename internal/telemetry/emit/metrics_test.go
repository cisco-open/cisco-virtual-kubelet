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

package emit

import (
	"context"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/classifier"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/mapper"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestEmitGaugeRecordsValue(t *testing.T) {
	emitter, reader := newTestMetricsEmitter(t)
	value := 12.5

	emitted := emitter.Emit(context.Background(), []mapper.MappedEvent{
		metricEvent("interface.rx.kbps", classifier.MetricKindGauge, value, "rx"),
	})
	if emitted != 1 {
		t.Fatalf("Emit()=%d, want 1", emitted)
	}

	rm := collectMetrics(t, reader)
	dp, ok := gaugePoint(rm, "interface.rx.kbps")
	if !ok {
		t.Fatalf("missing gauge metric")
	}
	if dp.Value != value {
		t.Fatalf("gauge value=%f, want %f", dp.Value, value)
	}
	if got := attrString(dp.Attributes, "device"); got != "edge-01" {
		t.Fatalf("device attr=%q, want edge-01", got)
	}
	if got := attrString(dp.Attributes, "cisco.device.name"); got != "" {
		t.Fatalf("cisco.device.name data-point attr=%q, want absent", got)
	}
}

func TestEmitSumComputesDeltaFromCumulative(t *testing.T) {
	emitter, reader := newTestMetricsEmitter(t, sdkmetric.WithTemporalitySelector(sdkmetric.DeltaTemporalitySelector))
	first := 100.0
	second := 250.0

	if emitted := emitter.Emit(context.Background(), []mapper.MappedEvent{
		metricEvent("interface.in.octets", classifier.MetricKindSum, first, "if-in-octets"),
	}); emitted != 1 {
		t.Fatalf("first Emit()=%d, want 1", emitted)
	}
	firstRM := collectMetrics(t, reader)
	firstPoint, ok := sumPoint(firstRM, "interface.in.octets")
	if !ok || firstPoint.Value != 100 {
		t.Fatalf("first sum point=%+v ok=%t, want 100", firstPoint, ok)
	}

	if emitted := emitter.Emit(context.Background(), []mapper.MappedEvent{
		metricEvent("interface.in.octets", classifier.MetricKindSum, second, "if-in-octets"),
	}); emitted != 1 {
		t.Fatalf("second Emit()=%d, want 1", emitted)
	}
	secondRM := collectMetrics(t, reader)
	secondPoint, ok := sumPoint(secondRM, "interface.in.octets")
	if !ok || secondPoint.Value != 150 {
		t.Fatalf("second sum point=%+v ok=%t, want 150", secondPoint, ok)
	}
}

func TestCounterResetSkipsNegativeDelta(t *testing.T) {
	emitter, reader := newTestMetricsEmitter(t, sdkmetric.WithTemporalitySelector(sdkmetric.DeltaTemporalitySelector))

	if emitted := emitter.Emit(context.Background(), []mapper.MappedEvent{
		metricEvent("interface.in.octets", classifier.MetricKindSum, 250, "if-in-octets"),
	}); emitted != 1 {
		t.Fatalf("first Emit()=%d, want 1", emitted)
	}
	_ = collectMetrics(t, reader)

	if emitted := emitter.Emit(context.Background(), []mapper.MappedEvent{
		metricEvent("interface.in.octets", classifier.MetricKindSum, 30, "if-in-octets"),
	}); emitted != 0 {
		t.Fatalf("reset Emit()=%d, want 0", emitted)
	}
	rm := collectMetrics(t, reader)
	if point, ok := sumPoint(rm, "interface.in.octets"); ok && point.Value < 0 {
		t.Fatalf("negative counter delta emitted: %+v", point)
	}
	resetPoint, ok := intSumPoint(rm, counterResetsSelfMetric)
	if !ok || resetPoint.Value != 1 {
		t.Fatalf("counter reset self metric=%+v ok=%t, want 1", resetPoint, ok)
	}
}

func TestInstrumentMemoization(t *testing.T) {
	emitter, _ := newTestMetricsEmitter(t)

	events := []mapper.MappedEvent{
		metricEvent("memory.used", classifier.MetricKindGauge, 10, "memory-used"),
		metricEvent("memory.used", classifier.MetricKindGauge, 20, "memory-used"),
	}
	if emitted := emitter.Emit(context.Background(), events); emitted != 2 {
		t.Fatalf("Emit()=%d, want 2", emitted)
	}
	if got := emitter.instrumentCount(); got != 1 {
		t.Fatalf("instrumentCount()=%d, want 1", got)
	}
}

func TestInstrumentCapDropsSurfaceSelfMetric(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	self := NewSelfMetrics(provider)
	emitter := NewMetricsEmitter(provider,
		WithMaxInstruments(1),
		WithMetricsSelfMetrics(self),
	)

	if emitted := emitter.Emit(context.Background(), []mapper.MappedEvent{
		metricEvent("memory.used", classifier.MetricKindGauge, 10, "memory-used"),
	}); emitted != 1 {
		t.Fatalf("first Emit()=%d, want 1", emitted)
	}
	if emitted := emitter.Emit(context.Background(), []mapper.MappedEvent{
		metricEvent("memory.allocated", classifier.MetricKindGauge, 20, "memory-alloc"),
	}); emitted != 0 {
		t.Fatalf("second Emit()=%d, want 0 (cap blocks new instrument)", emitted)
	}
	if got := self.CapDropTotal(); got != 1 {
		t.Fatalf("CapDropTotal()=%d, want 1", got)
	}

	rm := collectMetrics(t, reader)
	point, ok := intSumPoint(rm, instrumentCapDropsSelfMetric)
	if !ok || point.Value != 1 {
		t.Fatalf("cap drop self metric=%+v ok=%t, want 1", point, ok)
	}
	if got := attrString(point.Attributes, "metric"); got != "memory.allocated" {
		t.Fatalf("metric attr=%q, want memory.allocated", got)
	}
}

func TestSetMaxInstrumentsRaisesCap(t *testing.T) {
	emitter, _ := newTestMetricsEmitter(t)
	emitter.SetMaxInstruments(2)

	events := []mapper.MappedEvent{
		metricEvent("a", classifier.MetricKindGauge, 1, "a"),
		metricEvent("b", classifier.MetricKindGauge, 2, "b"),
		metricEvent("c", classifier.MetricKindGauge, 3, "c"),
	}
	if emitted := emitter.Emit(context.Background(), events); emitted != 2 {
		t.Fatalf("Emit()=%d, want 2 (third blocked by cap=2)", emitted)
	}
	emitter.SetMaxInstruments(3)
	if emitted := emitter.Emit(context.Background(), []mapper.MappedEvent{
		metricEvent("c", classifier.MetricKindGauge, 3, "c"),
	}); emitted != 1 {
		t.Fatalf("after raising cap Emit()=%d, want 1", emitted)
	}
}

func newTestMetricsEmitter(t *testing.T, opts ...sdkmetric.ManualReaderOption) (*MetricsEmitter, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader(opts...)
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
	})
	return NewMetricsEmitter(provider), reader
}

func metricEvent(name string, kind classifier.MetricKind, value float64, series string) mapper.MappedEvent {
	return mapper.MappedEvent{
		Signal:      mapper.SignalKindMetric,
		Name:        name,
		MetricKind:  kind,
		NumberValue: &value,
		SeriesKey:   series,
		Resource: []mapper.KeyValue{
			{Key: "device", Value: "edge-01"},
			{Key: "subscription", Value: "interfaces"},
			{Key: "cisco.device.name", Value: "edge-01"},
		},
		Attributes: []mapper.KeyValue{
			{Key: "ifname", Value: "GigabitEthernet1"},
		},
	}
}

func collectMetrics(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect(): %v", err)
	}
	return rm
}

func gaugePoint(rm metricdata.ResourceMetrics, name string) (metricdata.DataPoint[float64], bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, metric := range sm.Metrics {
			if metric.Name != name {
				continue
			}
			data, ok := metric.Data.(metricdata.Gauge[float64])
			if !ok || len(data.DataPoints) == 0 {
				return metricdata.DataPoint[float64]{}, false
			}
			return data.DataPoints[0], true
		}
	}
	return metricdata.DataPoint[float64]{}, false
}

func sumPoint(rm metricdata.ResourceMetrics, name string) (metricdata.DataPoint[float64], bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, metric := range sm.Metrics {
			if metric.Name != name {
				continue
			}
			data, ok := metric.Data.(metricdata.Sum[float64])
			if !ok || len(data.DataPoints) == 0 {
				return metricdata.DataPoint[float64]{}, false
			}
			return data.DataPoints[0], true
		}
	}
	return metricdata.DataPoint[float64]{}, false
}

func intSumPoint(rm metricdata.ResourceMetrics, name string) (metricdata.DataPoint[int64], bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, metric := range sm.Metrics {
			if metric.Name != name {
				continue
			}
			data, ok := metric.Data.(metricdata.Sum[int64])
			if !ok || len(data.DataPoints) == 0 {
				return metricdata.DataPoint[int64]{}, false
			}
			return data.DataPoints[0], true
		}
	}
	return metricdata.DataPoint[int64]{}, false
}

func attrString(set attribute.Set, key string) string {
	value, ok := set.Value(attribute.Key(key))
	if !ok {
		return ""
	}
	return value.AsString()
}
