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
	"testing"
	"time"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/mapper"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestEmitsRecoverySpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	emitter := NewTracesEmitter(tp, nil, []configv1alpha1.Transition{operStatusTransition()})

	t1 := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(42 * time.Second)
	emitted := emitter.Emit(context.Background(), []mapper.MappedEvent{
		traceEvent("Gi1", "down", t1),
		traceEvent("Gi1", "up", t2),
	})
	if emitted != 1 {
		t.Fatalf("emitted=%d, want 1", emitted)
	}
	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans=%d, want 1", len(spans))
	}
	span := spans[0]
	if span.Name() != "state.transition.interfaces.interface.state.oper-status" {
		t.Fatalf("span name=%q", span.Name())
	}
	if !span.StartTime().Equal(t1) || !span.EndTime().Equal(t2) {
		t.Fatalf("span interval=[%s,%s], want [%s,%s]", span.StartTime(), span.EndTime(), t1, t2)
	}
	attrs := attrsByKey(span.Attributes())
	if attrs["from-state"] != "down" || attrs["to-state"] != "up" || attrs["name"] != "Gi1" {
		t.Fatalf("span attrs=%v", attrs)
	}
}

func TestSuppressesHealthyOnly(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	emitter := NewTracesEmitter(tp, nil, []configv1alpha1.Transition{operStatusTransition()})

	t1 := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	emitted := emitter.Emit(context.Background(), []mapper.MappedEvent{
		traceEvent("Gi1", "up", t1),
		traceEvent("Gi1", "up", t1.Add(time.Second)),
	})
	if emitted != 0 {
		t.Fatalf("emitted=%d, want 0", emitted)
	}
	if spans := recorder.Ended(); len(spans) != 0 {
		t.Fatalf("spans=%d, want 0", len(spans))
	}
}

func TestPerKeyTracking(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	emitter := NewTracesEmitter(tp, nil, []configv1alpha1.Transition{operStatusTransition()})

	t1 := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	emitted := emitter.Emit(context.Background(), []mapper.MappedEvent{
		traceEvent("Gi1", "down", t1),
		traceEvent("Gi2", "down", t1.Add(time.Second)),
		traceEvent("Gi1", "up", t1.Add(2*time.Second)),
	})
	if emitted != 1 {
		t.Fatalf("emitted=%d, want 1", emitted)
	}
	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans=%d, want 1", len(spans))
	}
	if got := attrsByKey(spans[0].Attributes())["name"]; got != "Gi1" {
		t.Fatalf("first recovered key=%q, want Gi1", got)
	}

	emitted = emitter.Emit(context.Background(), []mapper.MappedEvent{
		traceEvent("Gi2", "up", t1.Add(3*time.Second)),
	})
	if emitted != 1 {
		t.Fatalf("second emitted=%d, want 1", emitted)
	}
	spans = recorder.Ended()
	if len(spans) != 2 {
		t.Fatalf("spans=%d, want 2", len(spans))
	}
	if got := attrsByKey(spans[1].Attributes())["name"]; got != "Gi2" {
		t.Fatalf("second recovered key=%q, want Gi2", got)
	}
}

func operStatusTransition() configv1alpha1.Transition {
	return configv1alpha1.Transition{
		Path:            "/interfaces/interface[name=*]/state/oper-status",
		HealthyValues:   []string{"up"},
		UnhealthyValues: []string{"down"},
	}
}

func traceEvent(name, value string, ts time.Time) mapper.MappedEvent {
	path := "/interfaces/interface[name=" + name + "]/state/oper-status"
	return mapper.MappedEvent{
		Signal:        mapper.SignalKindTrace,
		Name:          "/interfaces/interface/state/oper-status",
		CanonicalPath: path,
		Body:          value,
		Timestamp:     ts,
		SeriesKey:     "subscription\x00" + path + "\x00name=" + name,
		Resource: []mapper.KeyValue{
			{Key: "device", Value: "edge-01"},
			{Key: "subscription", Value: "interfaces"},
		},
		Attributes: []mapper.KeyValue{
			{Key: "name", Value: name},
			{Key: "cisco.gnmi.path", Value: path},
		},
	}
}

func attrsByKey(attrs []attribute.KeyValue) map[string]string {
	out := map[string]string{}
	for _, attr := range attrs {
		out[string(attr.Key)] = attr.Value.AsString()
	}
	return out
}
