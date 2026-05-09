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

package otelproviders

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	// register the gzip compressor under the name "gzip" so OTLP exporters
	// can request it via WithCompressor.
	_ "google.golang.org/grpc/encoding/gzip"
)

const defaultShutdownTimeout = 5 * time.Second
const exporterFailuresMetric = "cisco_vk_telemetry_exporter_failures_total"

type Config struct {
	OTLPEndpoint    string
	Insecure        bool
	Headers         map[string]string
	ResourceAttrs   map[string]string
	ShutdownTimeout time.Duration
}

type Providers struct {
	Tracer trace.TracerProvider
	Meter  metric.MeterProvider
	Logger otellog.LoggerProvider
}

func New(ctx context.Context, cfg Config) (*Providers, func(context.Context) error, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	exportFailures := newExportFailureRecorder()
	res, err := resource(ctx, cfg.ResourceAttrs)
	if err != nil {
		return nil, nil, err
	}
	// OTLP gRPC tuning. Telemetry batches with cumulative interface counters
	// regularly exceed the otel-collector's default 4 MiB receive limit; pin
	// gzip on every outgoing call (the OTLP exporters' WithCompressor option
	// is silently ignored when WithGRPCConn supplies the connection) and lift
	// the per-call envelope to 64 MiB on both sides.
	const maxOTLPMsgBytes = 64 * 1024 * 1024
	conn, err := grpc.NewClient(
		endpointTarget(cfg.OTLPEndpoint),
		grpc.WithTransportCredentials(transportCredentials(cfg.Insecure)),
		grpc.WithDefaultCallOptions(
			grpc.UseCompressor("gzip"),
			grpc.MaxCallSendMsgSize(maxOTLPMsgBytes),
			grpc.MaxCallRecvMsgSize(maxOTLPMsgBytes),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create OTLP gRPC connection: %w", err)
	}

	// gzip-compress OTLP payloads. Telemetry batches with cumulative interface
	// counters routinely exceed the otel-collector's default 4 MiB receive limit;
	// gzip typically yields ~20x compression on counter-heavy payloads.
	traceExp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithGRPCConn(conn),
		otlptracegrpc.WithHeaders(cfg.Headers),
		otlptracegrpc.WithCompressor("gzip"),
	)
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	countedTraceExp := failureCountingTraceExporter{SpanExporter: traceExp, recorder: exportFailures}
	metricExp, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithGRPCConn(conn),
		otlpmetricgrpc.WithHeaders(cfg.Headers),
		otlpmetricgrpc.WithCompressor("gzip"),
	)
	if err != nil {
		_ = traceExp.Shutdown(ctx)
		_ = conn.Close()
		return nil, nil, fmt.Errorf("create OTLP metric exporter: %w", err)
	}
	countedMetricExp := failureCountingMetricExporter{Exporter: metricExp, recorder: exportFailures}
	logExp, err := otlploggrpc.New(ctx,
		otlploggrpc.WithGRPCConn(conn),
		otlploggrpc.WithHeaders(cfg.Headers),
		otlploggrpc.WithCompressor("gzip"),
	)
	if err != nil {
		_ = metricExp.Shutdown(ctx)
		_ = traceExp.Shutdown(ctx)
		_ = conn.Close()
		return nil, nil, fmt.Errorf("create OTLP log exporter: %w", err)
	}
	countedLogExp := failureCountingLogExporter{Exporter: logExp, recorder: exportFailures}

	// Explicit exporter boundary controls. SDK defaults silently drop sends
	// under MDT-rate sustained load; pin queue depth, batch sizing, and
	// export timeouts so behaviour is predictable across deployments.
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(countedTraceExp,
			sdktrace.WithMaxQueueSize(8192),
			sdktrace.WithMaxExportBatchSize(512),
			sdktrace.WithBatchTimeout(5*time.Second),
			sdktrace.WithExportTimeout(30*time.Second),
		),
		sdktrace.WithResource(res),
	)
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(countedMetricExp,
			sdkmetric.WithInterval(15*time.Second),
			sdkmetric.WithTimeout(30*time.Second),
		)),
		sdkmetric.WithResource(res),
	)
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(countedLogExp,
			sdklog.WithMaxQueueSize(8192),
			sdklog.WithExportMaxBatchSize(512),
			sdklog.WithExportInterval(5*time.Second),
			sdklog.WithExportTimeout(30*time.Second),
		)),
		sdklog.WithResource(res),
	)
	exportFailures.register(mp)

	providers := &Providers{Tracer: tp, Meter: mp, Logger: lp}
	timeout := cfg.ShutdownTimeout
	if timeout <= 0 {
		timeout = defaultShutdownTimeout
	}
	shutdown := func(parent context.Context) error {
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithTimeout(parent, timeout)
		defer cancel()
		errCh := make(chan error, 3)
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			errCh <- tp.Shutdown(ctx)
		}()
		go func() {
			defer wg.Done()
			errCh <- mp.Shutdown(ctx)
		}()
		go func() {
			defer wg.Done()
			errCh <- lp.Shutdown(ctx)
		}()
		wg.Wait()
		close(errCh)
		var err error
		for e := range errCh {
			err = errors.Join(err, e)
		}
		return errors.Join(err, conn.Close())
	}
	return providers, shutdown, nil
}

func resource(ctx context.Context, attrs map[string]string) (*sdkresource.Resource, error) {
	kvs := make([]attribute.KeyValue, 0, len(attrs))
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		kvs = append(kvs, attribute.String(k, attrs[k]))
	}
	res, err := sdkresource.New(ctx,
		sdkresource.WithTelemetrySDK(),
		sdkresource.WithFromEnv(),
		sdkresource.WithAttributes(kvs...),
	)
	if err != nil {
		return nil, fmt.Errorf("create OTEL resource: %w", err)
	}
	return res, nil
}

func endpointTarget(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "localhost:4317"
	}
	if u, err := url.Parse(endpoint); err == nil && u.Scheme != "" && u.Host != "" {
		return u.Host
	}
	return strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://")
}

type exportFailureKey struct {
	signal string
	reason string
}

type exportFailureRecorder struct {
	mu     sync.Mutex
	counts map[exportFailureKey]*atomic.Int64
}

func newExportFailureRecorder() *exportFailureRecorder {
	return &exportFailureRecorder{counts: map[exportFailureKey]*atomic.Int64{}}
}

func (r *exportFailureRecorder) register(provider metric.MeterProvider) {
	if r == nil || provider == nil {
		return
	}
	meter := provider.Meter("github.com/cisco/virtual-kubelet-cisco/internal/otelproviders")
	_, _ = meter.Int64ObservableCounter(
		exporterFailuresMetric,
		metric.WithDescription("OTLP exporter failures observed by cisco-vk"),
		metric.WithInt64Callback(func(ctx context.Context, observer metric.Int64Observer) error {
			for _, sample := range r.samples() {
				observer.Observe(sample.value, metric.WithAttributes(
					attribute.String("signal", sample.key.signal),
					attribute.String("reason", sample.key.reason),
				))
			}
			return nil
		}),
	)
}

func (r *exportFailureRecorder) record(signal string, err error) {
	if r == nil || err == nil {
		return
	}
	key := exportFailureKey{signal: signal, reason: exportFailureReason(err)}
	r.mu.Lock()
	count, ok := r.counts[key]
	if !ok {
		count = &atomic.Int64{}
		r.counts[key] = count
	}
	r.mu.Unlock()
	count.Add(1)
}

type exportFailureSample struct {
	key   exportFailureKey
	value int64
}

func (r *exportFailureRecorder) samples() []exportFailureSample {
	r.mu.Lock()
	defer r.mu.Unlock()
	samples := make([]exportFailureSample, 0, len(r.counts))
	for key, count := range r.counts {
		samples = append(samples, exportFailureSample{key: key, value: count.Load()})
	}
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].key.signal == samples[j].key.signal {
			return samples[i].key.reason < samples[j].key.reason
		}
		return samples[i].key.signal < samples[j].key.signal
	})
	return samples
}

func exportFailureReason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	}
	code := status.Code(err)
	if code != codes.OK && code != codes.Unknown {
		return strings.ToLower(code.String())
	}
	return "export_error"
}

type failureCountingTraceExporter struct {
	sdktrace.SpanExporter
	recorder *exportFailureRecorder
}

func (e failureCountingTraceExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	err := e.SpanExporter.ExportSpans(ctx, spans)
	e.recorder.record("traces", err)
	return err
}

type failureCountingMetricExporter struct {
	sdkmetric.Exporter
	recorder *exportFailureRecorder
}

func (e failureCountingMetricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	err := e.Exporter.Export(ctx, rm)
	e.recorder.record("metrics", err)
	return err
}

type failureCountingLogExporter struct {
	sdklog.Exporter
	recorder *exportFailureRecorder
}

func (e failureCountingLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	err := e.Exporter.Export(ctx, records)
	e.recorder.record("logs", err)
	return err
}

func transportCredentials(insecureTransport bool) credentials.TransportCredentials {
	if insecureTransport {
		return insecure.NewCredentials()
	}
	return credentials.NewClientTLSFromCert(nil, "")
}
