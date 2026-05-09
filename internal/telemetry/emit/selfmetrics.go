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
	"sync/atomic"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

const (
	activeStreamsSelfMetric      = "cisco_vk_telemetry_active_streams"
	streamReconnectsSelfMetric   = "cisco_vk_telemetry_stream_reconnects_total"
	logRecordsSelfMetric         = "cisco_vk_telemetry_log_records_emitted_total"
	instrumentCapDropsSelfMetric = "cisco_vk_telemetry_instrument_cap_drops_total"
)

// SelfMetrics holds the OTel instruments shared across emitters and the stream
// manager. A nil receiver is safe — every method short-circuits — so callers
// don't need to gate every call site.
type SelfMetrics struct {
	activeStreams      metric.Int64UpDownCounter
	streamReconnects   metric.Int64Counter
	logRecords         metric.Int64Counter
	instrumentCapDrops metric.Int64Counter

	capDropTotal atomic.Int64
}

// NewSelfMetrics registers the shared self-metric instruments on the supplied
// MeterProvider. A nil provider yields a noop SelfMetrics so production wiring
// can pass an unset provider without conditional checks.
func NewSelfMetrics(provider metric.MeterProvider) *SelfMetrics {
	if provider == nil {
		provider = noop.NewMeterProvider()
	}
	meter := provider.Meter(meterName)
	active, _ := meter.Int64UpDownCounter(activeStreamsSelfMetric)
	reconnects, _ := meter.Int64Counter(streamReconnectsSelfMetric)
	logs, _ := meter.Int64Counter(logRecordsSelfMetric)
	capDrops, _ := meter.Int64Counter(instrumentCapDropsSelfMetric)
	return &SelfMetrics{
		activeStreams:      active,
		streamReconnects:   reconnects,
		logRecords:         logs,
		instrumentCapDrops: capDrops,
	}
}

// AddActiveStreams adjusts the active-stream gauge by delta (typically +1 on
// stream open, -1 on stream close).
func (s *SelfMetrics) AddActiveStreams(ctx context.Context, delta int64, device, subscription string) {
	if s == nil || s.activeStreams == nil {
		return
	}
	s.activeStreams.Add(ctx, delta, metric.WithAttributes(
		attribute.String("device", device),
		attribute.String("subscription", subscription),
	))
}

// IncStreamReconnects counts one Subscribe RPC reconnect attempt.
func (s *SelfMetrics) IncStreamReconnects(ctx context.Context, device, subscription string) {
	if s == nil || s.streamReconnects == nil {
		return
	}
	s.streamReconnects.Add(ctx, 1, metric.WithAttributes(
		attribute.String("device", device),
		attribute.String("subscription", subscription),
	))
}

// AddLogRecords counts emitted OTel log records.
func (s *SelfMetrics) AddLogRecords(ctx context.Context, n int64, device, subscription string) {
	if s == nil || s.logRecords == nil || n <= 0 {
		return
	}
	s.logRecords.Add(ctx, n, metric.WithAttributes(
		attribute.String("device", device),
		attribute.String("subscription", subscription),
	))
}

// IncInstrumentCapDrops counts one mapped event dropped because the metric
// instrument cap blocked instrument creation. Callers pass the metric name
// that would have been registered so dashboards can pinpoint hot offenders.
func (s *SelfMetrics) IncInstrumentCapDrops(ctx context.Context, device, subscription, metricName string) {
	if s == nil || s.instrumentCapDrops == nil {
		return
	}
	s.capDropTotal.Add(1)
	s.instrumentCapDrops.Add(ctx, 1, metric.WithAttributes(
		attribute.String("device", device),
		attribute.String("subscription", subscription),
		attribute.String("metric", metricName),
	))
}

// CapDropTotal returns the cumulative count of cap-drop events the receiver
// has observed. Reconcilers use this to surface the InstrumentCapExceeded
// status condition without scraping the OTel pipeline.
func (s *SelfMetrics) CapDropTotal() int64 {
	if s == nil {
		return 0
	}
	return s.capDropTotal.Load()
}
