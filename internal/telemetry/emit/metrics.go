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
	"strings"
	"sync"
	"unicode"

	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/classifier"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/mapper"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

const (
	meterName                   = "cisco_vk_telemetry"
	maxMetricInstruments        = 1024
	metricPointsSelfMetric      = "cisco_vk_telemetry_metric_points_emitted_total"
	classifierDecisionsMetric   = "cisco_vk_telemetry_classifier_decisions_total"
	counterResetsSelfMetric     = "cisco_vk_telemetry_counter_resets_total"
	classifierDecisionUnclassed = "unclassified"
)

type MetricsEmitter struct {
	meter metric.Meter

	mu          sync.Mutex
	gauges      map[string]metric.Float64Gauge
	counters    map[string]metric.Float64Counter
	instruments map[instrumentKey]struct{}
	lastValues  map[string]float64

	metricPointsTotal        metric.Int64Counter
	classifierDecisionsTotal metric.Int64Counter
	counterResetsTotal       metric.Int64Counter
}

type instrumentKey struct {
	name string
	kind classifier.MetricKind
}

func NewMetricsEmitter(provider metric.MeterProvider) *MetricsEmitter {
	if provider == nil {
		provider = noop.NewMeterProvider()
	}
	meter := provider.Meter(meterName)
	metricPoints, _ := meter.Int64Counter(metricPointsSelfMetric)
	classifierDecisions, _ := meter.Int64Counter(classifierDecisionsMetric)
	counterResets, _ := meter.Int64Counter(counterResetsSelfMetric)
	return &MetricsEmitter{
		meter:                    meter,
		gauges:                   map[string]metric.Float64Gauge{},
		counters:                 map[string]metric.Float64Counter{},
		instruments:              map[instrumentKey]struct{}{},
		lastValues:               map[string]float64{},
		metricPointsTotal:        metricPoints,
		classifierDecisionsTotal: classifierDecisions,
		counterResetsTotal:       counterResets,
	}
}

func (e *MetricsEmitter) Emit(ctx context.Context, events []mapper.MappedEvent) int {
	if e == nil || e.meter == nil {
		return 0
	}
	emitted := 0
	for _, event := range events {
		if event.Signal != mapper.SignalKindMetric {
			continue
		}
		e.recordClassifierDecision(ctx, event)
		if event.NumberValue == nil {
			continue
		}
		kind := event.MetricKind
		if kind == "" {
			kind = classifier.MetricKindGauge
		}
		attrs := attributeSet(event.Resource, event.Attributes)
		switch kind {
		case classifier.MetricKindSum:
			if e.emitSum(ctx, event, attrs) {
				emitted++
			}
		default:
			if e.emitGauge(ctx, event, attrs) {
				emitted++
			}
		}
	}
	return emitted
}

func (e *MetricsEmitter) emitGauge(ctx context.Context, event mapper.MappedEvent, attrs attribute.Set) bool {
	gauge, ok := e.gauge(event)
	if !ok {
		return false
	}
	gauge.Record(ctx, *event.NumberValue, metric.WithAttributeSet(attrs))
	e.recordPoint(ctx, event, classifier.MetricKindGauge)
	return true
}

func (e *MetricsEmitter) emitSum(ctx context.Context, event mapper.MappedEvent, attrs attribute.Set) bool {
	counter, ok := e.counter(event)
	if !ok {
		return false
	}
	series := event.SeriesKey
	if series == "" {
		series = event.Name
	}
	current := *event.NumberValue
	e.mu.Lock()
	last, known := e.lastValues[series]
	if !known {
		e.lastValues[series] = current
		e.mu.Unlock()
		counter.Add(ctx, current, metric.WithAttributeSet(attrs))
		e.recordPoint(ctx, event, classifier.MetricKindSum)
		return true
	}
	delta := current - last
	e.lastValues[series] = current
	e.mu.Unlock()
	if delta < 0 {
		e.recordCounterReset(ctx, event)
		return false
	}
	counter.Add(ctx, delta, metric.WithAttributeSet(attrs))
	e.recordPoint(ctx, event, classifier.MetricKindSum)
	return true
}

func (e *MetricsEmitter) gauge(event mapper.MappedEvent) (metric.Float64Gauge, bool) {
	name := metricName(event)
	e.mu.Lock()
	defer e.mu.Unlock()
	if gauge := e.gauges[name]; gauge != nil {
		return gauge, true
	}
	key := instrumentKey{name: name, kind: classifier.MetricKindGauge}
	if _, ok := e.instruments[key]; !ok && len(e.instruments) >= maxMetricInstruments {
		return nil, false
	}
	gauge, err := e.meter.Float64Gauge(name, metric.WithUnit(event.Unit))
	if err != nil {
		return nil, false
	}
	e.gauges[name] = gauge
	e.instruments[key] = struct{}{}
	return gauge, true
}

func (e *MetricsEmitter) counter(event mapper.MappedEvent) (metric.Float64Counter, bool) {
	name := metricName(event)
	e.mu.Lock()
	defer e.mu.Unlock()
	if counter := e.counters[name]; counter != nil {
		return counter, true
	}
	key := instrumentKey{name: name, kind: classifier.MetricKindSum}
	if _, ok := e.instruments[key]; !ok && len(e.instruments) >= maxMetricInstruments {
		return nil, false
	}
	counter, err := e.meter.Float64Counter(name, metric.WithUnit(event.Unit))
	if err != nil {
		return nil, false
	}
	e.counters[name] = counter
	e.instruments[key] = struct{}{}
	return counter, true
}

func (e *MetricsEmitter) recordPoint(ctx context.Context, event mapper.MappedEvent, kind classifier.MetricKind) {
	if e.metricPointsTotal == nil {
		return
	}
	e.metricPointsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("device", attrValue(event.Resource, "device")),
		attribute.String("subscription", attrValue(event.Resource, "subscription")),
		attribute.String("kind", string(kind)),
	))
}

func (e *MetricsEmitter) recordClassifierDecision(ctx context.Context, event mapper.MappedEvent) {
	if e.classifierDecisionsTotal == nil {
		return
	}
	kind := string(event.MetricKind)
	switch event.MetricKind {
	case classifier.MetricKindGauge, classifier.MetricKindSum:
	default:
		kind = classifierDecisionUnclassed
	}
	e.classifierDecisionsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("device", attrValue(event.Resource, "device")),
		attribute.String("subscription", attrValue(event.Resource, "subscription")),
		attribute.String("kind", kind),
	))
}

func (e *MetricsEmitter) recordCounterReset(ctx context.Context, event mapper.MappedEvent) {
	if e.counterResetsTotal == nil {
		return
	}
	metricLabel := event.Name
	if metricLabel == "" {
		metricLabel = event.CanonicalPath
	}
	e.counterResetsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("device", attrValue(event.Resource, "device")),
		attribute.String("subscription", attrValue(event.Resource, "subscription")),
		attribute.String("metric", metricLabel),
	))
}

func (e *MetricsEmitter) instrumentCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.instruments)
}

func metricName(event mapper.MappedEvent) string {
	name := event.Name
	if name == "" {
		name = event.CanonicalPath
	}
	return sanitizeMetricName(name)
}

func sanitizeMetricName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "cisco.gnmi.metric"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '_', r == '.', r == '-':
			b.WriteRune(r)
		case r == '/':
			b.WriteByte('.')
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		out = "metric"
	}
	first, _ := utf8FirstRune(out)
	if !unicode.IsLetter(first) {
		out = "cisco.gnmi." + out
	}
	if len(out) > 255 {
		out = out[:255]
		out = strings.TrimRight(out, "._-")
	}
	return out
}

func utf8FirstRune(s string) (rune, bool) {
	for _, r := range s {
		return r, true
	}
	return 0, false
}

func attributeSet(resource []mapper.KeyValue, attrs []mapper.KeyValue) attribute.Set {
	kvs := make([]attribute.KeyValue, 0, len(resource)+len(attrs))
	for _, attr := range resource {
		kvs = append(kvs, attribute.String(attr.Key, attr.Value))
	}
	for _, attr := range attrs {
		kvs = append(kvs, attribute.String(attr.Key, attr.Value))
	}
	return attribute.NewSet(kvs...)
}

func attrValue(attrs []mapper.KeyValue, key string) string {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value
		}
	}
	if key == "device" {
		for _, attr := range attrs {
			if attr.Key == "cisco.device.name" {
				return attr.Value
			}
		}
	}
	return ""
}
