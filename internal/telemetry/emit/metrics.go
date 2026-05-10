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
	defaultMaxMetricInstruments = 1024
	metricPointsSelfMetric      = "cisco_vk_telemetry_metric_points_emitted_total"
	classifierDecisionsMetric   = "cisco_vk_telemetry_classifier_decisions_total"
	counterResetsSelfMetric     = "cisco_vk_telemetry_counter_resets_total"
	classifierDecisionUnclassed = "unclassified"
)

type MetricsEmitter struct {
	meter metric.Meter

	maxInstruments int
	self           *SelfMetrics

	mu          sync.Mutex
	gauges      map[string]metric.Float64Gauge
	counters    map[string]metric.Float64Counter
	instruments map[instrumentKey]struct{}
	lastValues  map[string]float64

	metricPointsTotal        metric.Int64Counter
	classifierDecisionsTotal metric.Int64Counter
	counterResetsTotal       metric.Int64Counter
}

// MetricsEmitterOption configures a MetricsEmitter at construction.
type MetricsEmitterOption func(*MetricsEmitter)

// WithMaxInstruments overrides the default instrument-name cap. Values <= 0
// keep the default.
func WithMaxInstruments(n int) MetricsEmitterOption {
	return func(e *MetricsEmitter) {
		if n > 0 {
			e.maxInstruments = n
		}
	}
}

// WithMetricsSelfMetrics wires the shared SelfMetrics so cap-drop events are
// reported on the OTel pipeline and surfaced via CapDropTotal.
func WithMetricsSelfMetrics(self *SelfMetrics) MetricsEmitterOption {
	return func(e *MetricsEmitter) {
		e.self = self
	}
}

type instrumentKey struct {
	name string
	kind classifier.MetricKind
}

func NewMetricsEmitter(provider metric.MeterProvider, opts ...MetricsEmitterOption) *MetricsEmitter {
	if provider == nil {
		provider = noop.NewMeterProvider()
	}
	meter := provider.Meter(meterName)
	metricPoints, _ := meter.Int64Counter(metricPointsSelfMetric)
	classifierDecisions, _ := meter.Int64Counter(classifierDecisionsMetric)
	counterResets, _ := meter.Int64Counter(counterResetsSelfMetric)
	e := &MetricsEmitter{
		meter:                    meter,
		maxInstruments:           defaultMaxMetricInstruments,
		gauges:                   map[string]metric.Float64Gauge{},
		counters:                 map[string]metric.Float64Counter{},
		instruments:              map[instrumentKey]struct{}{},
		lastValues:               map[string]float64{},
		metricPointsTotal:        metricPoints,
		classifierDecisionsTotal: classifierDecisions,
		counterResetsTotal:       counterResets,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(e)
		}
	}
	return e
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
	gauge, ok := e.gauge(ctx, event)
	if !ok {
		return false
	}
	gauge.Record(ctx, *event.NumberValue, metric.WithAttributeSet(attrs))
	e.recordPoint(ctx, event, classifier.MetricKindGauge)
	return true
}

func (e *MetricsEmitter) emitSum(ctx context.Context, event mapper.MappedEvent, attrs attribute.Set) bool {
	counter, ok := e.counter(ctx, event)
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

func (e *MetricsEmitter) gauge(ctx context.Context, event mapper.MappedEvent) (metric.Float64Gauge, bool) {
	name := metricName(event)
	e.mu.Lock()
	if gauge := e.gauges[name]; gauge != nil {
		e.mu.Unlock()
		return gauge, true
	}
	key := instrumentKey{name: name, kind: classifier.MetricKindGauge}
	if _, ok := e.instruments[key]; !ok && len(e.instruments) >= e.maxInstruments {
		e.mu.Unlock()
		e.recordCapDrop(ctx, event, name)
		return nil, false
	}
	gauge, err := e.meter.Float64Gauge(name, metric.WithUnit(event.Unit))
	if err != nil {
		e.mu.Unlock()
		return nil, false
	}
	e.gauges[name] = gauge
	e.instruments[key] = struct{}{}
	e.mu.Unlock()
	return gauge, true
}

func (e *MetricsEmitter) counter(ctx context.Context, event mapper.MappedEvent) (metric.Float64Counter, bool) {
	name := metricName(event)
	e.mu.Lock()
	if counter := e.counters[name]; counter != nil {
		e.mu.Unlock()
		return counter, true
	}
	key := instrumentKey{name: name, kind: classifier.MetricKindSum}
	if _, ok := e.instruments[key]; !ok && len(e.instruments) >= e.maxInstruments {
		e.mu.Unlock()
		e.recordCapDrop(ctx, event, name)
		return nil, false
	}
	counter, err := e.meter.Float64Counter(name, metric.WithUnit(event.Unit))
	if err != nil {
		e.mu.Unlock()
		return nil, false
	}
	e.counters[name] = counter
	e.instruments[key] = struct{}{}
	e.mu.Unlock()
	return counter, true
}

func (e *MetricsEmitter) recordCapDrop(ctx context.Context, event mapper.MappedEvent, name string) {
	if e.self == nil {
		return
	}
	e.self.IncInstrumentCapDrops(ctx,
		attrValue(event.Resource, "device"),
		attrValue(event.Resource, "subscription"),
		name,
	)
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

// SetMaxInstruments updates the instrument-name cap. Existing registered
// instruments are preserved; only future name registrations are subject to
// the new cap. Values <= 0 are ignored so partial spec data doesn't shrink
// a working cap.
func (e *MetricsEmitter) SetMaxInstruments(n int) {
	if e == nil || n <= 0 {
		return
	}
	e.mu.Lock()
	e.maxInstruments = n
	e.mu.Unlock()
}

// MaxInstruments returns the current cap.
func (e *MetricsEmitter) MaxInstruments() int {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.maxInstruments
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
		if mapper.IsForbiddenDataPointAttribute(attr.Key) {
			continue
		}
		kvs = append(kvs, attribute.String(attr.Key, attr.Value))
	}
	for _, attr := range attrs {
		if mapper.IsForbiddenDataPointAttribute(attr.Key) {
			continue
		}
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
