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
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/correlation"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/mapper"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
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
	if attrs["from-state"] != "down" || attrs["to-state"] != "up" || attrs["name"] != "Gi1" ||
		attrs["cvk.evidence.type"] != "state_transition" {
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

func TestRecoverySpanUsesContextLink(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	emitter := NewTracesEmitter(tp, nil, []configv1alpha1.Transition{operStatusTransition()})
	source := testSpanContext(t)
	ctx := correlation.WithLifecycleID(correlation.WithSpanLink(context.Background(), source), "release-181")

	t1 := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	emitted := emitter.Emit(ctx, []mapper.MappedEvent{
		traceEvent("Gi1", "down", t1),
		traceEvent("Gi1", "up", t1.Add(time.Second)),
	})
	if emitted != 1 {
		t.Fatalf("emitted=%d, want 1", emitted)
	}
	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans=%d, want 1", len(spans))
	}
	if parent := spans[0].Parent(); parent.IsValid() {
		t.Fatalf("parent=%s, want new root span", parent.SpanID())
	}
	links := spans[0].Links()
	if len(links) != 1 || links[0].SpanContext.SpanID() != source.SpanID() {
		t.Fatalf("links=%+v, want source span link", links)
	}
	if got := attrsByKey(spans[0].Attributes())[correlation.LifecycleIDAttribute]; got != "release-181" {
		t.Fatalf("lifecycle attribute=%q, want release-181", got)
	}
	attrs := attrsByKey(links[0].Attributes)
	if attrs["cvk.correlation.type"] != "span_link" ||
		attrs["cvk.correlation.source"] != "cache" ||
		attrs["cvk.cause.trace_id"] != source.TraceID().String() ||
		attrs["cvk.cause.span_id"] != source.SpanID().String() {
		t.Fatalf("link attrs=%v", attrs)
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

func testSpanContext(t *testing.T) trace.SpanContext {
	t.Helper()
	tid, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	sid, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
	})
}

func attrsByKey(attrs []attribute.KeyValue) map[string]string {
	out := map[string]string{}
	for _, attr := range attrs {
		out[string(attr.Key)] = attr.Value.AsString()
	}
	return out
}
