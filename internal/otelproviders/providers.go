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
	"time"

	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

const defaultShutdownTimeout = 5 * time.Second

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
	res, err := resource(ctx, cfg.ResourceAttrs)
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient(endpointTarget(cfg.OTLPEndpoint), grpc.WithTransportCredentials(transportCredentials(cfg.Insecure)))
	if err != nil {
		return nil, nil, fmt.Errorf("create OTLP gRPC connection: %w", err)
	}

	traceExp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn), otlptracegrpc.WithHeaders(cfg.Headers))
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	metricExp, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithGRPCConn(conn), otlpmetricgrpc.WithHeaders(cfg.Headers))
	if err != nil {
		_ = traceExp.Shutdown(ctx)
		_ = conn.Close()
		return nil, nil, fmt.Errorf("create OTLP metric exporter: %w", err)
	}
	logExp, err := otlploggrpc.New(ctx, otlploggrpc.WithGRPCConn(conn), otlploggrpc.WithHeaders(cfg.Headers))
	if err != nil {
		_ = metricExp.Shutdown(ctx)
		_ = traceExp.Shutdown(ctx)
		_ = conn.Close()
		return nil, nil, fmt.Errorf("create OTLP log exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(traceExp), sdktrace.WithResource(res))
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)),
		sdkmetric.WithResource(res),
	)
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
		sdklog.WithResource(res),
	)

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

func transportCredentials(insecureTransport bool) credentials.TransportCredentials {
	if insecureTransport {
		return insecure.NewCredentials()
	}
	return credentials.NewClientTLSFromCert(nil, "")
}
