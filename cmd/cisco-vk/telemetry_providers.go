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
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	otellog "go.opentelemetry.io/otel/log"
	otelmetric "go.opentelemetry.io/otel/metric"
	oteltrace "go.opentelemetry.io/otel/trace"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/otelproviders"
)

const (
	envOTELExporterOTLPHeaders = "OTEL_EXPORTER_OTLP_HEADERS"
	envCVKResourceAttributes   = "CVK_RESOURCE_ATTRIBUTES"
)

const (
	processRoleController       = "controller"
	processRoleVKProvider       = "vk-provider"
	processRoleTelemetryEmitter = "telemetry-emitter"
)

// buildTelemetryProviders constructs the per-device telemetry OTel provider
// stack that the IOSXETelemetryReconciler uses for log emission and
// CVK-self metrics. Endpoint and TLS controls follow the standard
// OpenTelemetry SDK environment-variable contract:
//
//	OTEL_EXPORTER_OTLP_ENDPOINT      (e.g. otelcol.observability:4317)
//	OTEL_EXPORTER_OTLP_INSECURE      ("true" disables TLS; default false)
//	OTEL_EXPORTER_OTLP_HEADERS       (serialized metadata headers)
//	CVK_RESOURCE_ATTRIBUTES          (serialized extra resource attributes)
//
// When the endpoint is unset this returns a nil Providers value and emission
// is suppressed by noop emitter fallbacks.
func buildTelemetryProviders(ctx context.Context, deviceName string, opts configReconcilerOptions) (*otelproviders.Providers, func(context.Context) error, error) {
	if opts.Spec == nil {
		return nil, nil, fmt.Errorf("nil DeviceSpec")
	}
	deviceAddress := opts.Spec.Address
	base := map[string]string{
		"service.name":         "cisco-vk-telemetry",
		"service.instance.id":  telemetryServiceInstanceID(deviceName),
		"cvk.process.role":     processRoleTelemetryEmitter,
		"host.name":            deviceName,
		"net.peer.name":        deviceAddress,
		"cisco.device.name":    deviceName,
		"cisco.device.address": deviceAddress,
		"cvk.driver.kind":      string(opts.Spec.Driver),
	}
	return buildOTelProviders(ctx, base)
}

// buildVKProviders constructs the per-device Virtual Kubelet provider's OTel
// provider. It is intentionally separate from buildTelemetryProviders because
// the same pod emits two logical services: the provider control-plane spans
// (`cisco-vk-vk`) and the MDT emitter signals (`cisco-vk-telemetry`).
func buildVKProviders(ctx context.Context, deviceName string, spec *ciskov1.DeviceSpec) (*otelproviders.Providers, func(context.Context) error, error) {
	if spec == nil {
		return nil, nil, fmt.Errorf("nil DeviceSpec")
	}
	base := map[string]string{
		"service.name":         "cisco-vk-vk",
		"service.instance.id":  serviceInstanceID(deviceName),
		"cvk.process.role":     processRoleVKProvider,
		"host.name":            deviceName,
		"net.peer.name":        spec.Address,
		"cisco.device.name":    deviceName,
		"cisco.device.address": spec.Address,
		"cvk.driver.kind":      string(spec.Driver),
	}
	return buildOTelProviders(ctx, base)
}

// buildControllerProviders constructs the system-controller OTel provider.
// Controller-manager spans and metrics carry the cluster pod identity rather
// than a device peer address.
func buildControllerProviders(ctx context.Context) (*otelproviders.Providers, func(context.Context) error, error) {
	hostName := os.Getenv("NODE_NAME")
	if hostName == "" {
		hostName, _ = os.Hostname()
	}
	base := map[string]string{
		"service.name":        "cisco-vk-controller",
		"service.instance.id": serviceInstanceID("controller"),
		"cvk.process.role":    processRoleController,
		"host.name":           hostName,
	}
	return buildOTelProviders(ctx, base)
}

func buildOTelProviders(ctx context.Context, base map[string]string) (*otelproviders.Providers, func(context.Context) error, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		return nil, nil, nil
	}
	insecure, _ := strconv.ParseBool(os.Getenv("OTEL_EXPORTER_OTLP_INSECURE"))
	// The OpenTelemetry env var spec requires a URL scheme on
	// OTEL_EXPORTER_OTLP_ENDPOINT. Operators frequently set a bare host:port,
	// which the SDK's url.Parse rejects with "first path segment in URL cannot
	// contain colon". Normalize before constructing providers and write the
	// fixed value back to the env so any SDK code path that re-reads the env
	// sees the same value.
	if normalized := normalizeOTLPEndpoint(endpoint, insecure); normalized != endpoint {
		_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", normalized)
		endpoint = normalized
	}
	headers, err := serializedStringMap(os.Getenv(envOTELExporterOTLPHeaders))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", envOTELExporterOTLPHeaders, err)
	}
	for k, v := range k8sResourceAttributesFromEnv() {
		base[k] = v
	}
	resourceAttrs, err := telemetryResourceAttributes(base)
	if err != nil {
		return nil, nil, err
	}
	cfg := otelproviders.Config{
		OTLPEndpoint:  endpoint,
		Insecure:      insecure,
		Headers:       headers,
		ResourceAttrs: resourceAttrs,
	}
	return otelproviders.New(ctx, cfg)
}

// k8sResourceAttributesFromEnv reads downward-API-injected env vars (POD_NAME,
// POD_NAMESPACE, NODE_NAME, POD_UID) and returns OTel SemConv resource
// attributes. Empty values are dropped so unset env vars do not produce empty
// labels. The Helm chart wires these via the downward API on every pod that
// emits telemetry; outside Kubernetes (e.g. unit tests) the map is empty and
// the SDK falls back to telemetry.sdk.* identity only.
func k8sResourceAttributesFromEnv() map[string]string {
	out := map[string]string{}
	if v := strings.TrimSpace(os.Getenv("POD_NAME")); v != "" {
		out["k8s.pod.name"] = v
	}
	if v := strings.TrimSpace(os.Getenv("POD_NAMESPACE")); v != "" {
		out["k8s.namespace.name"] = v
	}
	if v := strings.TrimSpace(os.Getenv("NODE_NAME")); v != "" {
		out["k8s.node.name"] = v
	}
	if v := strings.TrimSpace(os.Getenv("POD_UID")); v != "" {
		out["k8s.pod.uid"] = v
	}
	return out
}

// serviceInstanceID returns a stable identifier for this telemetry-emitting
// process. POD_UID is preferred (uniquely identifies the process incarnation
// across restarts on the same node); falling back to POD_NAME if UID is
// unavailable; falling back to deviceName as a last resort so non-k8s
// deployments still get an instance.id distinct from service.name.
func serviceInstanceID(deviceName string) string {
	if v := strings.TrimSpace(os.Getenv("POD_UID")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("POD_NAME")); v != "" {
		return v
	}
	return deviceName
}

func telemetryServiceInstanceID(deviceName string) string {
	base := serviceInstanceID(deviceName)
	if deviceName == "" {
		return base
	}
	return base + ":" + deviceName
}

func telemetryResourceAttributes(base map[string]string) (map[string]string, error) {
	attrs := make(map[string]string, len(base))
	for k, v := range base {
		attrs[k] = v
	}
	extra, err := serializedStringMap(os.Getenv(envCVKResourceAttributes))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", envCVKResourceAttributes, err)
	}
	for k, v := range extra {
		attrs[k] = v
	}
	return attrs, nil
}

// normalizeOTLPEndpoint ensures the OTEL_EXPORTER_OTLP_ENDPOINT value carries a
// URL scheme. If the input already starts with http:// or https:// it is
// returned unchanged. Otherwise the function prepends http:// when insecure is
// true and https:// when it is false, matching the OTel SDK's expectations.
func normalizeOTLPEndpoint(raw string, insecure bool) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return raw
	}
	lowered := strings.ToLower(trimmed)
	if strings.HasPrefix(lowered, "http://") || strings.HasPrefix(lowered, "https://") {
		return trimmed
	}
	if insecure {
		return "http://" + trimmed
	}
	return "https://" + trimmed
}

func serializedStringMap(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return nil, nil
	}
	if strings.HasPrefix(raw, "{") {
		var out map[string]string
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("expected key=value entry %q", pair)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("empty key in %q", pair)
		}
		out[key] = strings.TrimSpace(value)
	}
	return out, nil
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

// telemetryMeterProvider returns the MeterProvider from the optional Providers
// value built by buildTelemetryProviders. Returns nil when no providers were
// constructed; the MetricsEmitter handles a nil provider with a noop fallback.
func telemetryMeterProvider(p *otelproviders.Providers) otelmetric.MeterProvider {
	if p == nil {
		return nil
	}
	return p.Meter
}

// telemetryTracerProvider returns the TracerProvider from the optional
// Providers value built by buildTelemetryProviders. Returns nil when no
// providers were constructed; the TracesEmitter handles a nil provider with a
// noop fallback.
func telemetryTracerProvider(p *otelproviders.Providers) oteltrace.TracerProvider {
	if p == nil {
		return nil
	}
	return p.Tracer
}
