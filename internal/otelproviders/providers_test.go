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
	"net"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	logscol "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metricscol "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecol "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestProvidersConstructAndShutdown(t *testing.T) {
	collector, endpoint := newFakeOTLPCollector(t)
	ctx := context.Background()

	providers, shutdown, err := New(ctx, Config{
		OTLPEndpoint:    endpoint,
		Insecure:        true,
		ResourceAttrs:   map[string]string{"service.name": "cisco-vk-test"},
		ShutdownTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if providers == nil || providers.Tracer == nil || providers.Meter == nil || providers.Logger == nil {
		t.Fatalf("providers=%+v, want tracer/meter/logger", providers)
	}

	_, span := providers.Tracer.Tracer("providers-test").Start(ctx, "phase2-span")
	span.End()

	counter, err := providers.Meter.Meter("providers-test").Int64Counter(
		"phase2.counter",
		metric.WithDescription("phase2 test counter"),
	)
	if err != nil {
		t.Fatalf("Int64Counter: %v", err)
	}
	counter.Add(ctx, 1)

	var record otellog.Record
	record.SetTimestamp(time.Now())
	record.SetBody(otellog.StringValue("phase2 log"))
	record.SetSeverity(otellog.SeverityInfo)
	record.SetSeverityText("INFO")
	providers.Logger.Logger("providers-test").Emit(ctx, record)

	done := make(chan error, 1)
	go func() {
		done <- shutdown(context.Background())
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown timed out")
	}

	logs, metrics, traces := collector.Counts()
	if logs == 0 || metrics == 0 || traces == 0 {
		t.Fatalf("collector counts logs=%d metrics=%d traces=%d, want all > 0", logs, metrics, traces)
	}
}

func TestExportFailureRecorderPublishesObservableCounter(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	recorder := newExportFailureRecorder()
	recorder.register(mp)

	recorder.record("traces", status.Error(codes.Unavailable, "collector down"))
	recorder.record("traces", status.Error(codes.Unavailable, "collector down"))
	recorder.record("logs", context.DeadlineExceeded)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != exporterFailuresMetric {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s data type = %T, want metricdata.Sum[int64]", exporterFailuresMetric, m.Data)
			}
			for _, dp := range sum.DataPoints {
				signal := attrString(dp.Attributes, "signal")
				reason := attrString(dp.Attributes, "reason")
				got[signal+"/"+reason] = dp.Value
			}
		}
	}
	if got["traces/unavailable"] != 2 {
		t.Fatalf("traces/unavailable=%d, want 2; all=%v", got["traces/unavailable"], got)
	}
	if got["logs/deadline_exceeded"] != 1 {
		t.Fatalf("logs/deadline_exceeded=%d, want 1; all=%v", got["logs/deadline_exceeded"], got)
	}
}

// TestRegisterProcessInfoGaugeEmitsBareGauge guards against a regression where
// the gauge carried data-point attributes that duplicated resource attribute
// keys. The OTel Collector's prometheus exporter rejects such data points with
// "duplicate label names in constant and variable labels" when
// resource_to_telemetry_conversion is enabled, which then drops every cisco_vk_*
// series after the gauge in the same scrape.
func TestRegisterProcessInfoGaugeEmitsBareGauge(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	registerProcessInfoGauge(mp, map[string]string{
		"service.name":        "cisco-vk-controller",
		"service.instance.id": "pod-123",
		"cvk.process.role":    "controller",
		"cvk.driver.kind":     "iosxe",
		"cluster":             "prod",
		"env":                 "test",
		"owner":               "netops",
	})

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var found bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != processInfoMetric {
				continue
			}
			found = true
			gauge, ok := m.Data.(metricdata.Gauge[int64])
			if !ok {
				t.Fatalf("%s data type = %T, want metricdata.Gauge[int64]", processInfoMetric, m.Data)
			}
			if len(gauge.DataPoints) != 1 {
				t.Fatalf("datapoints=%d want 1", len(gauge.DataPoints))
			}
			dp := gauge.DataPoints[0]
			if dp.Value != 1 {
				t.Fatalf("%s value=%d want 1", processInfoMetric, dp.Value)
			}
			// Identity must travel on the resource, not on the data point.
			// Each forbidden key here would, when promoted to a constant
			// label by resource_to_telemetry_conversion, collide with the
			// same-named data-point label and trip the prometheus exporter.
			for _, k := range []string{
				"service_name",
				"service_instance_id",
				"cvk_process_role",
				"cvk_driver_kind",
				"cluster",
				"env",
				"owner",
			} {
				if _, ok := dp.Attributes.Value(attribute.Key(k)); ok {
					t.Fatalf("data-point attribute %q must not be set; identity belongs on the resource", k)
				}
			}
		}
	}
	if !found {
		t.Fatalf("missing %s", processInfoMetric)
	}
}

func attrString(attrs attribute.Set, key string) string {
	value, ok := attrs.Value(attribute.Key(key))
	if !ok {
		return ""
	}
	return value.AsString()
}

type fakeOTLPCollector struct {
	mu      sync.Mutex
	logs    int
	metrics int
	traces  int
}

func newFakeOTLPCollector(t *testing.T) (*fakeOTLPCollector, string) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	collector := &fakeOTLPCollector{}
	logscol.RegisterLogsServiceServer(srv, fakeLogsService{collector: collector})
	metricscol.RegisterMetricsServiceServer(srv, fakeMetricsService{collector: collector})
	tracecol.RegisterTraceServiceServer(srv, fakeTraceService{collector: collector})
	go func() {
		_ = srv.Serve(lis)
	}()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})
	return collector, lis.Addr().String()
}

type fakeLogsService struct {
	logscol.UnimplementedLogsServiceServer
	collector *fakeOTLPCollector
}

func (s fakeLogsService) Export(
	_ context.Context,
	req *logscol.ExportLogsServiceRequest,
) (*logscol.ExportLogsServiceResponse, error) {
	s.collector.mu.Lock()
	s.collector.logs += len(req.GetResourceLogs())
	s.collector.mu.Unlock()
	return &logscol.ExportLogsServiceResponse{}, nil
}

type fakeMetricsService struct {
	metricscol.UnimplementedMetricsServiceServer
	collector *fakeOTLPCollector
}

func (s fakeMetricsService) Export(
	_ context.Context,
	req *metricscol.ExportMetricsServiceRequest,
) (*metricscol.ExportMetricsServiceResponse, error) {
	s.collector.mu.Lock()
	s.collector.metrics += len(req.GetResourceMetrics())
	s.collector.mu.Unlock()
	return &metricscol.ExportMetricsServiceResponse{}, nil
}

type fakeTraceService struct {
	tracecol.UnimplementedTraceServiceServer
	collector *fakeOTLPCollector
}

func (s fakeTraceService) Export(
	_ context.Context,
	req *tracecol.ExportTraceServiceRequest,
) (*tracecol.ExportTraceServiceResponse, error) {
	s.collector.mu.Lock()
	s.collector.traces += len(req.GetResourceSpans())
	s.collector.mu.Unlock()
	return &tracecol.ExportTraceServiceResponse{}, nil
}

func (c *fakeOTLPCollector) Counts() (logs int, metrics int, traces int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.logs, c.metrics, c.traces
}
