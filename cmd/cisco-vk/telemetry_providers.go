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

package main

import (
	"context"
	"os"
	"strconv"

	otellog "go.opentelemetry.io/otel/log"

	"github.com/cisco/virtual-kubelet-cisco/internal/otelproviders"
)

// buildTelemetryProviders constructs the per-device telemetry OTel provider
// stack that the IOSXETelemetryReconciler uses for log emission and
// CVK-self metrics. Endpoint and TLS controls follow the standard
// OpenTelemetry SDK environment-variable contract:
//
//   OTEL_EXPORTER_OTLP_ENDPOINT      (e.g. otelcol.observability:4317)
//   OTEL_EXPORTER_OTLP_INSECURE      ("true" disables TLS; default false)
//
// When the endpoint is unset Phase 2 returns a nil Providers value and
// emission is suppressed (LogsEmitter falls back to a noop provider).
func buildTelemetryProviders(ctx context.Context, deviceName string, opts configReconcilerOptions) (*otelproviders.Providers, func(context.Context) error, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		return nil, nil, nil
	}
	insecure, _ := strconv.ParseBool(os.Getenv("OTEL_EXPORTER_OTLP_INSECURE"))
	cfg := otelproviders.Config{
		OTLPEndpoint: endpoint,
		Insecure:     insecure,
		ResourceAttrs: map[string]string{
			"service.name":         "cisco-vk-telemetry",
			"cisco.device.name":    deviceName,
			"cisco.device.address": opts.Spec.Address,
		},
	}
	return otelproviders.New(ctx, cfg)
}

// telemetryLoggerProvider returns the LoggerProvider from the optional
// Providers value built by buildTelemetryProviders. Returns nil when no
// providers were constructed; the LogsEmitter handles a nil provider by
// using its own noop fallback.
func telemetryLoggerProvider(p *otelproviders.Providers) otellog.LoggerProvider {
	if p == nil {
		return nil
	}
	return p.Logger
}
