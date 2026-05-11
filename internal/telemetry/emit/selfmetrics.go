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
	"sync"
	"sync/atomic"

	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/semconv"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

const (
	activeStreamsSelfMetric      = "cisco_vk_telemetry_active_streams"
	streamReconnectsSelfMetric   = "cisco_vk_telemetry_stream_reconnects_total"
	logRecordsSelfMetric         = "cisco_vk_telemetry_log_records_emitted_total"
	instrumentCapDropsSelfMetric = "cisco_vk_telemetry_instrument_cap_drops_total"
	processingDurationSelfMetric = "cisco_vk_telemetry_processing_duration_seconds"
	transitionsDroppedSelfMetric = "cisco_vk_telemetry_transitions_dropped_total"
	notifierDroppedSelfMetric    = "cisco_vk_telemetry_notifier_dropped_total"
	signalBudgetDroppedMetric    = "cisco_vk_signal_budget_dropped_total"
)

type budgetDropKey struct {
	signal string
	reason string
	device string
}

// BudgetDropSample is a cumulative in-process signal-budget drop sample.
type BudgetDropSample struct {
	Signal string
	Reason string
	Device string
	Count  int64
}

var (
	budgetDropMu      sync.Mutex
	budgetDropCounter metric.Int64Counter
	budgetDropCounts  = map[budgetDropKey]int64{}

	payloadBudgetMu     sync.Mutex
	payloadBudgetLimits = map[string]int64{}

	podStatusNotificationsSuppressed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cisco_vk_pod_status_notifications_suppressed_total",
			Help: "Number of provider pod status notifications suppressed because the meaningful pod state fingerprint did not change.",
		},
		[]string{"reason"},
	)
)

func init() {
	prometheus.MustRegister(podStatusNotificationsSuppressed)
}

// SelfMetrics holds the OTel instruments shared across emitters and the stream
// manager. A nil receiver is safe — every method short-circuits — so callers
// don't need to gate every call site.
type SelfMetrics struct {
	activeStreams      metric.Int64UpDownCounter
	streamReconnects   metric.Int64Counter
	logRecords         metric.Int64Counter
	instrumentCapDrops metric.Int64Counter
	processingDuration metric.Float64Histogram
	transitionsDropped metric.Int64Counter
	notifierDropped    metric.Int64Counter
	signalBudgetDrops  metric.Int64Counter

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
	// Processing-duration histogram covers mapper.Process + emitter dispatch
	// per gNMI Notification. Buckets span 100µs to 10s — the receiver's
	// equivalent metric uses 0.1ms–1000ms; we widen the upper bound because
	// pathological notifications (~6000 updates) measured ~2s end-to-end on
	// cat9k-smoke.
	duration, _ := meter.Float64Histogram(
		processingDurationSelfMetric,
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(
			0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10,
		),
	)
	transitionsDropped, _ := meter.Int64Counter(transitionsDroppedSelfMetric)
	notifierDropped, _ := meter.Int64Counter(notifierDroppedSelfMetric)
	signalBudgetDrops, _ := meter.Int64Counter(signalBudgetDroppedMetric)
	setBudgetDropCounter(signalBudgetDrops)
	return &SelfMetrics{
		activeStreams:      active,
		streamReconnects:   reconnects,
		logRecords:         logs,
		instrumentCapDrops: capDrops,
		processingDuration: duration,
		transitionsDropped: transitionsDropped,
		notifierDropped:    notifierDropped,
		signalBudgetDrops:  signalBudgetDrops,
	}
}

func setBudgetDropCounter(counter metric.Int64Counter) {
	budgetDropMu.Lock()
	budgetDropCounter = counter
	budgetDropMu.Unlock()
}

// RecordBudgetDropped increments the unified signal-budget drop counter.
func RecordBudgetDropped(ctx context.Context, signal, reason, device string) {
	if ctx == nil {
		ctx = context.Background()
	}
	if signal == "" {
		signal = "unknown"
	}
	if reason == "" {
		reason = "unknown"
	}
	if device == "" {
		device = "unknown"
	}
	key := budgetDropKey{signal: signal, reason: reason, device: device}
	budgetDropMu.Lock()
	budgetDropCounts[key]++
	counter := budgetDropCounter
	budgetDropMu.Unlock()
	if counter != nil {
		counter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("signal", signal),
			attribute.String("reason", reason),
			attribute.String("device", device),
		))
	}
}

// BudgetDropSnapshotForDevice returns cumulative budget drops for one device.
func BudgetDropSnapshotForDevice(device string) []BudgetDropSample {
	if device == "" {
		device = "unknown"
	}
	budgetDropMu.Lock()
	defer budgetDropMu.Unlock()
	out := make([]BudgetDropSample, 0)
	for key, count := range budgetDropCounts {
		if key.device != device || count <= 0 {
			continue
		}
		out = append(out, BudgetDropSample{
			Signal: key.signal,
			Reason: key.reason,
			Device: key.device,
			Count:  count,
		})
	}
	return out
}

// SetPayloadByteBudgetLimit records the effective per-device exporter payload
// byte budget. Exporter wrappers consult it at send time.
func SetPayloadByteBudgetLimit(device string, limit int) {
	if device == "" || limit <= 0 {
		return
	}
	payloadBudgetMu.Lock()
	payloadBudgetLimits[device] = int64(limit)
	payloadBudgetMu.Unlock()
}

// PayloadByteBudgetLimit returns the configured per-device exporter budget.
func PayloadByteBudgetLimit(device string, fallback int64) int64 {
	if device == "" {
		return fallback
	}
	payloadBudgetMu.Lock()
	limit := payloadBudgetLimits[device]
	payloadBudgetMu.Unlock()
	if limit <= 0 {
		return fallback
	}
	return limit
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
	RecordBudgetDropped(ctx, "metrics", "instrument_cap", device)
	if s == nil || s.instrumentCapDrops == nil {
		return
	}
	s.capDropTotal.Add(1)
	s.instrumentCapDrops.Add(ctx, 1, metric.WithAttributes(
		attribute.String("device", device),
		attribute.String("subscription", subscription),
		attribute.String("metric", metricName),
		attribute.String(semconv.CvkEvidenceType, semconv.EvidenceTypeMetricAnomaly),
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

// RecordProcessingDuration records mapper + emitter end-to-end time per
// gNMI Notification. duration is in seconds; callers measure with
// time.Since on a stable monotonic clock.
func (s *SelfMetrics) RecordProcessingDuration(ctx context.Context, seconds float64, device, subscription string) {
	if s == nil || s.processingDuration == nil {
		return
	}
	s.processingDuration.Record(ctx, seconds, metric.WithAttributes(
		attribute.String("device", device),
		attribute.String("subscription", subscription),
	))
}

// IncTransitionsDropped counts state-transition spans that were rate-limited
// out by the traces emitter. Operators read this to size the per-entity
// budget; high cumulative counts indicate routes/interfaces are flapping.
func (s *SelfMetrics) IncTransitionsDropped(ctx context.Context, device, subscription, path string) {
	RecordBudgetDropped(ctx, "traces", "transition_rate_limit", device)
	if s == nil || s.transitionsDropped == nil {
		return
	}
	s.transitionsDropped.Add(ctx, 1, metric.WithAttributes(
		attribute.String("device", device),
		attribute.String("subscription", subscription),
		attribute.String("path", path),
	))
}

// IncNotifierDropped counts app-hosting state events that could not be queued
// to the PodNotifier bridge. The bridge is intentionally lossy on overflow:
// the next status poll remains authoritative, while this counter exposes that
// push-based freshness is falling behind.
func (s *SelfMetrics) IncNotifierDropped(ctx context.Context, reason string) {
	if reason == "" {
		reason = "unknown"
	}
	RecordBudgetDropped(ctx, "podnotifier", reason, "unknown")
	if s == nil || s.notifierDropped == nil {
		return
	}
	s.notifierDropped.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
}

// IncPodStatusNotificationSuppressed counts a skipped PodNotifier callback.
func IncPodStatusNotificationSuppressed(reason string) {
	if reason == "" {
		reason = "unknown"
	}
	podStatusNotificationsSuppressed.WithLabelValues(reason).Inc()
}
