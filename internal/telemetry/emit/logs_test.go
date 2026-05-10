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

package emit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/mapper"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

func TestLogsEmitterEmitsStringLeaves(t *testing.T) {
	emitter, exporter := newCaptureEmitter(t)
	ts := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)

	emitted := emitter.Emit(context.Background(), []mapper.MappedEvent{{
		Signal:    mapper.SignalKindLog,
		Name:      "interfaces.interface.state.oper-status",
		Body:      "interface up",
		Severity:  mapper.SeverityInfo,
		Timestamp: ts,
	}})

	if emitted != 1 {
		t.Fatalf("Emit()=%d, want 1", emitted)
	}
	records := exporter.Records()
	if len(records) != 1 {
		t.Fatalf("records=%d, want 1", len(records))
	}
	if got := records[0].Body().AsString(); got != "interface up" {
		t.Fatalf("body=%q, want interface up", got)
	}
	if got := records[0].SeverityText(); got != string(mapper.SeverityInfo) {
		t.Fatalf("severity text=%q, want INFO", got)
	}
	if !records[0].Timestamp().Equal(ts) {
		t.Fatalf("timestamp=%s, want %s", records[0].Timestamp(), ts)
	}
	if got := records[0].EventName(); got != "cvk.interfaces.interface.state.oper-status" {
		t.Fatalf("event name=%q, want cvk.interfaces.interface.state.oper-status", got)
	}
}

func TestLogsEmitterDoesNotDoublePrefixEventName(t *testing.T) {
	emitter, exporter := newCaptureEmitter(t)

	emitted := emitter.Emit(context.Background(), []mapper.MappedEvent{{
		Signal:   mapper.SignalKindLog,
		Name:     "cvk.app-hosting.state",
		Body:     "DEPLOYED",
		Severity: mapper.SeverityInfo,
	}})
	if emitted != 1 {
		t.Fatalf("Emit()=%d, want 1", emitted)
	}
	records := exporter.Records()
	if len(records) != 1 {
		t.Fatalf("records=%d, want 1", len(records))
	}
	if got := records[0].EventName(); got != "cvk.app-hosting.state" {
		t.Fatalf("event name=%q, want cvk.app-hosting.state", got)
	}
}

func TestLogsEmitterEmitsDeletes(t *testing.T) {
	emitter, exporter := newCaptureEmitter(t)

	emitted := emitter.Emit(context.Background(), []mapper.MappedEvent{{
		Signal:   mapper.SignalKindLog,
		Body:     "deleted: /interfaces/interface[name=Gi1]",
		Severity: mapper.SeverityInfo,
		Attributes: []mapper.KeyValue{
			{Key: "cisco.gnmi.event", Value: "delete"},
		},
	}})

	if emitted != 1 {
		t.Fatalf("Emit()=%d, want 1", emitted)
	}
	records := exporter.Records()
	if len(records) != 1 {
		t.Fatalf("records=%d, want 1", len(records))
	}
	if got := records[0].Body().AsString(); got != "deleted: /interfaces/interface[name=Gi1]" {
		t.Fatalf("body=%q, want delete body", got)
	}
	if got := logAttr(records[0], "cisco.gnmi.event"); got != "delete" {
		t.Fatalf("cisco.gnmi.event=%q, want delete", got)
	}
}

func TestLogsEmitterPropagatesAttrs(t *testing.T) {
	emitter, exporter := newCaptureEmitter(t)

	emitted := emitter.Emit(context.Background(), []mapper.MappedEvent{
		{
			Signal: mapper.SignalKindMetric,
			Body:   "ignored metric-shaped event",
		},
		{
			Signal:   mapper.SignalKindLog,
			Body:     "line protocol UP",
			Severity: mapper.SeverityInfo,
			Resource: []mapper.KeyValue{
				{Key: "device", Value: "edge-01"},
				{Key: "subscription", Value: "interfaces"},
				{Key: "cisco.device.name", Value: "edge-01"},
			},
			Attributes: []mapper.KeyValue{
				{Key: "cisco.gnmi.path", Value: "/interfaces/interface/state/oper-status"},
				{Key: "name", Value: "GigabitEthernet1"},
				{Key: "owner", Value: "platform"},
			},
		},
	})

	if emitted != 1 {
		t.Fatalf("Emit()=%d, want 1", emitted)
	}
	records := exporter.Records()
	if len(records) != 1 {
		t.Fatalf("records=%d, want 1", len(records))
	}
	wantAttrs := map[string]string{
		"device":          "edge-01",
		"subscription":    "interfaces",
		"cisco.gnmi.path": "/interfaces/interface/state/oper-status",
		"name":            "GigabitEthernet1",
	}
	for key, want := range wantAttrs {
		if got := logAttr(records[0], key); got != want {
			t.Fatalf("%s=%q, want %q", key, got, want)
		}
	}
	for _, key := range []string{"cisco.device.name", "owner"} {
		if got := logAttr(records[0], key); got != "" {
			t.Fatalf("%s=%q, want absent", key, got)
		}
	}
}

type captureLogExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (e *captureLogExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range records {
		e.records = append(e.records, records[i].Clone())
	}
	return nil
}

func (e *captureLogExporter) Shutdown(context.Context) error {
	return nil
}

func (e *captureLogExporter) ForceFlush(context.Context) error {
	return nil
}

func (e *captureLogExporter) Records() []sdklog.Record {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]sdklog.Record(nil), e.records...)
}

func newCaptureEmitter(t *testing.T) (*LogsEmitter, *captureLogExporter) {
	t.Helper()
	exporter := &captureLogExporter{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)))
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
	})
	return NewLogsEmitter(provider), exporter
}

func logAttr(record sdklog.Record, key string) string {
	var out string
	record.WalkAttributes(func(kv log.KeyValue) bool {
		if kv.Key == key {
			out = kv.Value.AsString()
			return false
		}
		return true
	})
	return out
}
