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

// Live OTLP smoke test — gated behind RUN_LIVE_OTLP_SMOKE=1.
// Pushes one metric, one log, and one span to OTEL_EXPORTER_OTLP_ENDPOINT
// and asserts no errors. Use to confirm collector reachability.
package otelproviders

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/otel/log"
)

func TestLiveOTLPSmoke(t *testing.T) {
	if os.Getenv("RUN_LIVE_OTLP_SMOKE") != "1" {
		t.Skip("set RUN_LIVE_OTLP_SMOKE=1 to run live OTLP smoke test")
	}
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://192.168.129.23:4317"
	}
	cfg := Config{
		OTLPEndpoint: endpoint,
		Insecure:     true,
		ResourceAttrs: map[string]string{
			"service.name": "cisco-vk-otlp-smoke",
			"smoke.run":    fmt.Sprintf("%d", time.Now().Unix()),
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	providers, shutdown, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer shutdown(ctx)

	// Metric
	meter := providers.Meter.Meter("cisco_vk_otlp_smoke")
	counter, err := meter.Int64Counter("cisco_vk_smoke_test_counter")
	if err != nil {
		t.Fatalf("counter: %v", err)
	}
	counter.Add(ctx, 1)
	t.Logf("emitted metric cisco_vk_smoke_test_counter=1")

	// Log
	logger := providers.Logger.Logger("cisco_vk_otlp_smoke")
	var rec log.Record
	rec.SetTimestamp(time.Now())
	rec.SetSeverity(log.SeverityInfo)
	rec.SetSeverityText("INFO")
	rec.SetBody(log.StringValue("hello from cisco-vk otlp smoke test"))
	logger.Emit(ctx, rec)
	t.Logf("emitted log record")

	// Trace
	tracer := providers.Tracer.Tracer("cisco_vk_otlp_smoke")
	_, span := tracer.Start(ctx, "cisco-vk-smoke-span")
	time.Sleep(100 * time.Millisecond)
	span.End()
	t.Logf("emitted trace span cisco-vk-smoke-span")

	// Force flush by closing
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	t.Log("PASS: all 3 signals exported without error")
}
