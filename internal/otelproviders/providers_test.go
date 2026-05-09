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

	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	logscol "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metricscol "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecol "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
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
