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
	"testing"
	"time"

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
