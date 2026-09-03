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

package correlation

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	tracetest "go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

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

func TestTraceparentRoundTrip(t *testing.T) {
	sc := testSpanContext(t)
	raw := FormatTraceparent(sc)
	got, err := ParseTraceparent(raw)
	if err != nil {
		t.Fatalf("ParseTraceparent: %v", err)
	}
	if got.TraceID() != sc.TraceID() || got.SpanID() != sc.SpanID() || got.TraceFlags() != sc.TraceFlags() {
		t.Fatalf("round trip mismatch: %s -> %#v", raw, got)
	}
	if !got.IsRemote() {
		t.Fatal("parsed context should be remote")
	}
}

func TestSpanContextFromAnnotationsHonorsExpiry(t *testing.T) {
	sc := testSpanContext(t)
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	ann := map[string]string{
		TraceparentAnnotation:    FormatTraceparent(sc),
		TraceWindowEndAnnotation: now.Add(time.Minute).Format(time.RFC3339),
	}
	if _, ok := SpanContextFromAnnotations(ann, now); !ok {
		t.Fatal("expected valid context before expiry")
	}
	if _, ok := SpanContextFromAnnotations(ann, now.Add(2*time.Minute)); ok {
		t.Fatal("expected expired context to be rejected")
	}
	ann[TraceWindowEndAnnotation] = now.Add(MaxTraceWindow + time.Second).Format(time.RFC3339)
	if _, ok := SpanContextFromAnnotations(ann, now); ok {
		t.Fatal("expected unbounded future context to be rejected")
	}
}

func TestApplyAnnotationsUsesParentBeforeWindowAndLinkAfter(t *testing.T) {
	sc := testSpanContext(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	ann := map[string]string{
		TraceparentAnnotation:    FormatTraceparent(sc),
		TracestateAnnotation:     "vendor=opaque",
		TraceWindowEndAnnotation: now.Add(time.Minute).Format(time.RFC3339),
		LifecycleIDAnnotation:    "release-v2026.9.2-run-181",
	}

	parentCtx, got := ApplyAnnotations(context.Background(), ann, now)
	if got.Relationship != RelationshipParent || !got.HasPrimary() {
		t.Fatalf("fresh relationship=%q primary=%v", got.Relationship, got.HasPrimary())
	}
	parentSC := trace.SpanContextFromContext(parentCtx)
	if parentSC.TraceID() != sc.TraceID() || parentSC.TraceState().String() != "vendor=opaque" {
		t.Fatalf("unexpected parent span context: %#v", parentSC)
	}
	if links := SpanLinksFromContext(parentCtx); len(links) != 0 {
		t.Fatalf("fresh context has %d links, want 0", len(links))
	}

	linkCtx, got := ApplyAnnotations(context.Background(), ann, now.Add(2*time.Minute))
	if got.Relationship != RelationshipLink || !got.HasPrimary() {
		t.Fatalf("expired relationship=%q primary=%v", got.Relationship, got.HasPrimary())
	}
	if trace.SpanContextFromContext(linkCtx).IsValid() {
		t.Fatal("expired context must not install a remote parent")
	}
	links := SpanLinksFromContext(linkCtx)
	if len(links) != 1 || links[0].SpanContext.TraceID() != sc.TraceID() {
		t.Fatalf("expired links=%#v, want primary trace link", links)
	}
	if LifecycleIDFromContext(linkCtx) != ann[LifecycleIDAnnotation] {
		t.Fatalf("lifecycle id=%q", LifecycleIDFromContext(linkCtx))
	}
}

func TestApplyAnnotationsPreservesActiveParentAndLinksFreshCarrier(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	carrier := testSpanContext(t)
	activeTraceID, err := trace.TraceIDFromHex("11111111111111111111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	activeSpanID, err := trace.SpanIDFromHex("2222222222222222")
	if err != nil {
		t.Fatal(err)
	}
	active := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: activeTraceID, SpanID: activeSpanID, TraceFlags: trace.FlagsSampled,
	})
	parent := trace.ContextWithSpanContext(context.Background(), active)

	ctx, got := ApplyAnnotations(parent, map[string]string{
		TraceparentAnnotation:    FormatTraceparent(carrier),
		TraceWindowEndAnnotation: now.Add(time.Minute).Format(time.RFC3339),
	}, now)
	if got.Relationship != RelationshipLink || !got.HasPrimary() {
		t.Fatalf("relationship=%q primary=%v", got.Relationship, got.HasPrimary())
	}
	if actual := trace.SpanContextFromContext(ctx); actual.TraceID() != active.TraceID() || actual.SpanID() != active.SpanID() {
		t.Fatalf("active parent was replaced: got=%s/%s want=%s/%s",
			actual.TraceID(), actual.SpanID(), active.TraceID(), active.SpanID())
	}
	links := SpanLinksFromContext(ctx)
	if len(links) != 1 || links[0].SpanContext.TraceID() != carrier.TraceID() || links[0].SpanContext.SpanID() != carrier.SpanID() {
		t.Fatalf("fresh carrier links=%#v", links)
	}
}

func TestApplyAnnotationsDowngradesFarFutureWindowToLink(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	sc := testSpanContext(t)
	ctx, got := ApplyAnnotations(context.Background(), map[string]string{
		TraceparentAnnotation:    FormatTraceparent(sc),
		TraceWindowEndAnnotation: now.Add(MaxTraceWindow + time.Second).Format(time.RFC3339),
	}, now)
	if got.Relationship != RelationshipLink || !got.HasPrimary() {
		t.Fatalf("far-future carrier=%#v", got)
	}
	if trace.SpanContextFromContext(ctx).IsValid() {
		t.Fatal("far-future window installed a remote parent")
	}
	if links := SpanLinksFromContext(ctx); len(links) != 1 || links[0].SpanContext.TraceID() != sc.TraceID() {
		t.Fatalf("far-future links=%#v", links)
	}
	if got := SanitizedAnnotationsAt(map[string]string{
		TraceparentAnnotation:    FormatTraceparent(sc),
		TraceWindowEndAnnotation: now.Add(MaxTraceWindow + time.Second).Format(time.RFC3339),
	}, now); got != nil {
		t.Fatalf("far-future carrier copied to child: %#v", got)
	}
}

func TestApplyAnnotationsAlwaysLinksDistinctUpstream(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	primary := testSpanContext(t)
	upstreamTraceID, _ := trace.TraceIDFromHex("11111111111111111111111111111111")
	upstreamSpanID, _ := trace.SpanIDFromHex("2222222222222222")
	upstream := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: upstreamTraceID, SpanID: upstreamSpanID, TraceFlags: trace.FlagsSampled,
	})
	ctx, got := ApplyAnnotations(context.Background(), map[string]string{
		TraceparentAnnotation:         FormatTraceparent(primary),
		TraceWindowEndAnnotation:      now.Add(time.Minute).Format(time.RFC3339),
		UpstreamTraceparentAnnotation: FormatTraceparent(upstream),
	}, now)
	if !got.HasPrimary() || !got.HasUpstream() {
		t.Fatalf("contexts not accepted: %#v", got)
	}
	if trace.SpanContextFromContext(ctx).TraceID() != primary.TraceID() {
		t.Fatal("primary was not installed as parent")
	}
	links := SpanLinksFromContext(ctx)
	if len(links) != 1 || links[0].SpanContext.TraceID() != upstream.TraceID() {
		t.Fatalf("upstream links=%#v", links)
	}
}

func TestApplyAnnotationsRejectsMalformedAndUnboundedValues(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		ann  map[string]string
	}{
		{name: "uppercase traceparent", ann: map[string]string{
			TraceparentAnnotation:    "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01",
			TraceWindowEndAnnotation: now.Add(time.Minute).Format(time.RFC3339),
		}},
		{name: "abbreviated flags", ann: map[string]string{
			TraceparentAnnotation:    "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-1",
			TraceWindowEndAnnotation: now.Add(time.Minute).Format(time.RFC3339),
		}},
		{name: "unsupported flags", ann: map[string]string{
			TraceparentAnnotation:    "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-80",
			TraceWindowEndAnnotation: now.Add(time.Minute).Format(time.RFC3339),
		}},
		{name: "oversized tracestate", ann: map[string]string{
			TraceparentAnnotation:    FormatTraceparent(testSpanContext(t)),
			TracestateAnnotation:     string(make([]byte, MaxTracestateLength+1)),
			TraceWindowEndAnnotation: now.Add(time.Minute).Format(time.RFC3339),
		}},
		{name: "invalid window", ann: map[string]string{
			TraceparentAnnotation:    FormatTraceparent(testSpanContext(t)),
			TraceWindowEndAnnotation: "tomorrow",
		}},
		{name: "secret-like lifecycle", ann: map[string]string{
			LifecycleIDAnnotation: "token=do-not-propagate",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, got := ApplyAnnotations(context.Background(), tt.ann, now)
			if got.HasPrimary() || got.HasUpstream() {
				t.Fatalf("malformed carrier accepted: %#v", got)
			}
			if trace.SpanContextFromContext(ctx).IsValid() || len(SpanLinksFromContext(ctx)) != 0 {
				t.Fatal("malformed carrier modified trace context")
			}
			if got.LifecycleID != "" || LifecycleIDFromContext(ctx) != "" {
				t.Fatal("invalid lifecycle id propagated")
			}
		})
	}
}

func TestSanitizedAnnotationsCopiesOnlyValidatedAllowlist(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	sc := testSpanContext(t)
	ann := map[string]string{
		TraceparentAnnotation:         FormatTraceparent(sc),
		TracestateAnnotation:          "vendor=value",
		TraceWindowEndAnnotation:      now.Add(time.Minute).Format(time.RFC3339),
		UpstreamTraceparentAnnotation: FormatTraceparent(sc),
		LifecycleIDAnnotation:         "release-181",
		"example.com/password":        "do-not-copy",
	}
	got := SanitizedAnnotationsAt(ann, now)
	if len(got) != 5 {
		t.Fatalf("sanitized annotations=%#v", got)
	}
	if _, ok := got["example.com/password"]; ok {
		t.Fatal("non-allowlisted annotation copied")
	}
}

func TestStartAppliesLinksAndLifecycleAttribute(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	sc := testSpanContext(t)
	ctx := WithLifecycleID(WithSpanLink(context.Background(), sc), "release-181")
	_, span := Start(ctx, tp.Tracer("test"), "linked-work")
	span.End()
	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans=%d", len(ended))
	}
	if len(ended[0].Links()) != 1 || ended[0].Links()[0].SpanContext.TraceID() != sc.TraceID() {
		t.Fatalf("span links=%#v", ended[0].Links())
	}
	want := attribute.String(LifecycleIDAttribute, "release-181")
	found := false
	for _, kv := range ended[0].Attributes() {
		if kv == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("lifecycle attribute missing: %#v", ended[0].Attributes())
	}
}

func TestDetachedContextPreservesValuesAndLinksSchedulingSpan(t *testing.T) {
	type valueKey struct{}
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	parentCtx, parentSpan := tp.Tracer("test").Start(context.Background(), "scheduling")
	parentSC := parentSpan.SpanContext()
	parentCtx = context.WithValue(parentCtx, valueKey{}, "kept")
	parentCtx = WithLifecycleID(parentCtx, "release-181")
	parentCtx, cancel := context.WithTimeout(parentCtx, time.Minute)
	cancel()

	detached := DetachedContext(parentCtx)
	if got := detached.Value(valueKey{}); got != "kept" {
		t.Fatalf("context value=%v, want kept", got)
	}
	if got := LifecycleIDFromContext(detached); got != "release-181" {
		t.Fatalf("lifecycle id=%q, want release-181", got)
	}
	if _, ok := detached.Deadline(); ok {
		t.Fatal("detached context retained caller deadline")
	}
	if err := detached.Err(); err != nil {
		t.Fatalf("detached context retained caller cancellation: %v", err)
	}
	if sc := trace.SpanContextFromContext(detached); sc.IsValid() {
		t.Fatalf("detached context retained direct parent: %v", sc)
	}

	_, asyncSpan := Start(detached, tp.Tracer("test"), "async")
	asyncSpan.End()
	parentSpan.End()
	ended := recorder.Ended()
	if len(ended) != 2 {
		t.Fatalf("ended spans=%d, want 2", len(ended))
	}
	async := ended[0]
	if async.Name() != "async" {
		t.Fatalf("first span=%q, want async", async.Name())
	}
	if async.Parent().IsValid() {
		t.Fatalf("async span parent=%v, want root with link", async.Parent())
	}
	if len(async.Links()) != 1 ||
		async.Links()[0].SpanContext.TraceID() != parentSC.TraceID() ||
		async.Links()[0].SpanContext.SpanID() != parentSC.SpanID() {
		t.Fatalf("async links=%#v, want scheduling span %v", async.Links(), parentSC)
	}
}

func TestDetachedContextKeepsFreshAnnotationCarrierAsLink(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	scheduling := testSpanContext(t)
	detached := DetachedContext(trace.ContextWithSpanContext(context.Background(), scheduling))
	carrierTraceID, err := trace.TraceIDFromHex("11111111111111111111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	carrierSpanID, err := trace.SpanIDFromHex("2222222222222222")
	if err != nil {
		t.Fatal(err)
	}
	carrier := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: carrierTraceID, SpanID: carrierSpanID, TraceFlags: trace.FlagsSampled,
	})
	detached, got := ApplyAnnotations(detached, map[string]string{
		TraceparentAnnotation:    FormatTraceparent(carrier),
		TraceWindowEndAnnotation: now.Add(time.Minute).Format(time.RFC3339),
	}, now)
	if got.Relationship != RelationshipLink {
		t.Fatalf("relationship=%q, want link", got.Relationship)
	}
	if sc := trace.SpanContextFromContext(detached); sc.IsValid() {
		t.Fatalf("detached annotation installed direct parent: %v", sc)
	}
	links := SpanLinksFromContext(detached)
	if len(links) != 2 ||
		links[0].SpanContext.TraceID() != scheduling.TraceID() ||
		links[0].SpanContext.SpanID() != scheduling.SpanID() ||
		links[1].SpanContext.TraceID() != carrierTraceID {
		t.Fatalf("links=%#v, want scheduling and annotation links", links)
	}
}

func TestAsyncCorrelationRebasesOnConsumerCancellation(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	sourceCtx, sourceSpan := tp.Tracer("test").Start(context.Background(), "source")
	sourceSC := sourceSpan.SpanContext()
	sourceCtx = WithLifecycleID(sourceCtx, "release-181")
	sourceCtx, cancelSource := context.WithCancel(sourceCtx)
	snapshot := CaptureAsyncCorrelation(sourceCtx)
	cancelSource()

	consumerCtx, cancelConsumer := context.WithCancel(context.Background())
	restored := snapshot.Context(consumerCtx)
	if err := restored.Err(); err != nil {
		t.Fatalf("restored context retained source cancellation: %v", err)
	}
	if sc := trace.SpanContextFromContext(restored); sc.IsValid() {
		t.Fatalf("restored context retained a direct parent: %v", sc)
	}
	links := SpanLinksFromContext(restored)
	if len(links) != 1 ||
		links[0].SpanContext.TraceID() != sourceSC.TraceID() ||
		links[0].SpanContext.SpanID() != sourceSC.SpanID() {
		t.Fatalf("restored links=%#v, want source span %v", links, sourceSC)
	}
	if got := LifecycleIDFromContext(restored); got != "release-181" {
		t.Fatalf("restored lifecycle=%q, want release-181", got)
	}
	cancelConsumer()
	if err := restored.Err(); err != context.Canceled {
		t.Fatalf("restored context error=%v, want consumer cancellation", err)
	}
	sourceSpan.End()
}

func TestCacheExpiresAndEvicts(t *testing.T) {
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	cache := NewCache(time.Minute, 1, 0)
	cache.nowFunc = func() time.Time { return now }
	sc := testSpanContext(t)
	cache.Upsert("edge-01", "app-a", sc)
	cache.Upsert("edge-01", "app-b", sc)
	if _, _, ok := cache.Get("edge-01", "app-a"); ok {
		t.Fatal("expected app-a to be evicted by capacity")
	}
	if _, _, ok := cache.Get("edge-01", "app-b"); !ok {
		t.Fatal("expected app-b to be cached")
	}
	now = now.Add(2 * time.Minute)
	if _, _, ok := cache.Get("edge-01", "app-b"); ok {
		t.Fatal("expected app-b to expire")
	}
}

func TestCacheCarriesValidatedLifecycleID(t *testing.T) {
	cache := NewCache(time.Minute, 2, time.Second)
	sc := testSpanContext(t)
	cache.Upsert("edge-01", "app-a", sc, "release-181")
	gotSC, lifecycleID, _, ok := cache.GetWithLifecycle("edge-01", "app-a")
	if !ok || gotSC.TraceID() != sc.TraceID() || gotSC.SpanID() != sc.SpanID() || lifecycleID != "release-181" {
		t.Fatalf("cached correlation=(%v, %q, %t), want (%v, release-181, true)", gotSC, lifecycleID, ok, sc)
	}

	cache.Upsert("edge-01", "app-a", sc, "invalid lifecycle with spaces")
	_, lifecycleID, _, ok = cache.GetWithLifecycle("edge-01", "app-a")
	if !ok || lifecycleID != "" {
		t.Fatalf("invalid lifecycle persisted as %q, ok=%t", lifecycleID, ok)
	}
}

func TestCacheRelationshipWithinParentWindow(t *testing.T) {
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	cache := NewCache(15*time.Minute, 8, 30*time.Second)
	cache.nowFunc = func() time.Time { return now }
	cache.Upsert("edge-01", "app-a", testSpanContext(t))

	now = now.Add(29 * time.Second)
	_, age, ok := cache.Get("edge-01", "app-a")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got := cache.RelationshipForAge(age); got != RelationshipParent {
		t.Fatalf("relationship=%s, want %s", got, RelationshipParent)
	}
}

func TestCacheRelationshipAfterParentWindowWithinTTL(t *testing.T) {
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	cache := NewCache(15*time.Minute, 8, 30*time.Second)
	cache.nowFunc = func() time.Time { return now }
	cache.Upsert("edge-01", "app-a", testSpanContext(t))

	now = now.Add(2 * time.Minute)
	_, age, ok := cache.Get("edge-01", "app-a")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got := cache.RelationshipForAge(age); got != RelationshipLink {
		t.Fatalf("relationship=%s, want %s", got, RelationshipLink)
	}
}
